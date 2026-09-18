#!/usr/bin/env node
// SPDX-License-Identifier: AGPL-3.0-or-later

// Drives a real browser against a throwaway instance to check the behaviour
// that only exists once JavaScript runs.
//
// The Go tests cover what the server sends; this covers what the browser then
// does with it — Alpine initialising, the confirmation dialog standing in front
// of a submit, and the table filter hiding rows without a round trip. Those are
// exactly the things that cannot be verified over HTTP.
//
//   make test-browser
//
// Needs node and a Chromium-family browser. Exits 0 without running anything if
// either is missing, so it is safe to leave in a general check target.

import { spawn } from "node:child_process";
import { mkdtemp, rm, access } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const BROWSER_CANDIDATES = [
  process.env.CHROME,
  process.env.CHROMIUM,
  "/usr/bin/chromium",
  "/usr/bin/chromium-browser",
  "/usr/bin/google-chrome",
  "/usr/bin/google-chrome-stable",
].filter(Boolean);

const results = [];
let failures = 0;

function record(name, ok, detail = "") {
  results.push({ name, ok, detail });
  if (!ok) failures++;
}

async function exists(path) {
  try {
    await access(path);
    return true;
  } catch {
    return false;
  }
}

async function findBrowser() {
  for (const candidate of BROWSER_CANDIDATES) {
    if (candidate && (await exists(candidate))) return candidate;
  }
  return null;
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function waitForHttp(url, timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url);
      if (res.ok) return true;
    } catch {
      // not up yet
    }
    await sleep(200);
  }
  return false;
}

// --- a minimal Chrome DevTools Protocol client ------------------------------

class Page {
  constructor(socket) {
    this.socket = socket;
    this.nextId = 1;
    this.pending = new Map();
    this.listeners = new Map();
    this.console = [];
    this.dialogs = [];

    socket.addEventListener("message", (event) => {
      const message = JSON.parse(event.data);

      if (message.id && this.pending.has(message.id)) {
        const { resolve, reject } = this.pending.get(message.id);
        this.pending.delete(message.id);
        if (message.error) reject(new Error(message.error.message));
        else resolve(message.result);
        return;
      }

      if (message.method === "Runtime.consoleAPICalled") {
        this.console.push({
          level: message.params.type,
          text: (message.params.args || [])
            .map((a) => a.value ?? a.description ?? "")
            .join(" "),
        });
      }
      // If window.confirm were still in use this fires, which is precisely the
      // regression worth catching.
      if (message.method === "Page.javascriptDialogOpening") {
        this.dialogs.push(message.params.message);
      }

      const handlers = this.listeners.get(message.method) || [];
      for (const handler of handlers) handler(message.params);
    });
  }

  static async connect(debugPort) {
    const deadline = Date.now() + 15000;
    let target = null;

    while (Date.now() < deadline) {
      try {
        const res = await fetch(`http://127.0.0.1:${debugPort}/json/list`);
        const targets = await res.json();
        target = targets.find((t) => t.type === "page" && t.webSocketDebuggerUrl);
        if (target) break;
      } catch {
        // browser still starting
      }
      await sleep(200);
    }
    if (!target) throw new Error("no debuggable page target appeared");

    const socket = new WebSocket(target.webSocketDebuggerUrl);
    await new Promise((resolve, reject) => {
      socket.addEventListener("open", resolve, { once: true });
      socket.addEventListener("error", () => reject(new Error("CDP socket failed")), { once: true });
    });

    const page = new Page(socket);
    await page.send("Page.enable");
    await page.send("Runtime.enable");
    return page;
  }

  send(method, params = {}) {
    const id = this.nextId++;
    const promise = new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
    });
    // A navigation tears the evaluation context down, which can reject a
    // command whose caller has already moved on. Marking the rejection as
    // observed here keeps that expected outcome from killing the runner; the
    // caller still sees it if it is still awaiting.
    promise.catch(() => {});
    this.socket.send(JSON.stringify({ id, method, params }));
    return promise;
  }

  on(method, handler) {
    const handlers = this.listeners.get(method) || [];
    handlers.push(handler);
    this.listeners.set(method, handlers);
  }

  // evaluate runs an expression in the page, awaiting a promise if it returns
  // one, and hands back the value.
  async evaluate(script) {
    const result = await this.send("Runtime.evaluate", {
      expression: `(async () => { ${script} })()`,
      awaitPromise: true,
      returnByValue: true,
    });
    if (result.exceptionDetails) {
      const text = result.exceptionDetails.exception?.description || result.exceptionDetails.text;
      throw new Error(`page threw: ${text}`);
    }
    return result.result.value;
  }

  async goto(url) {
    await this.send("Page.navigate", { url });
    // waitFor takes an expression, not a statement: it is wrapped in a return.
    await this.waitFor("document.readyState === 'complete'");
    // Alpine initialises on DOMContentLoaded and then runs x-init; give it a tick.
    await sleep(250);
  }

  async waitFor(condition, timeoutMs = 10000, label = condition) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      try {
        if (await this.evaluate(`return !!(${condition});`)) return true;
      } catch {
        // navigation in flight
      }
      await sleep(100);
    }
    throw new Error(`timed out waiting for: ${label}`);
  }
}

// --- the checks -------------------------------------------------------------

async function main() {
  const browserPath = await findBrowser();
  if (!browserPath) {
    console.log("no Chromium-family browser found; skipping the browser checks");
    return 0;
  }

  const root = new URL("..", import.meta.url).pathname;
  const dataDir = await mkdtemp(join(tmpdir(), "imvault-browser-"));
  const port = 8123;
  const debugPort = 9223;
  const base = `http://127.0.0.1:${port}`;

  const build = spawn("go", ["build", "-o", join(dataDir, "imvault"), "./cmd/imvault"], {
    cwd: root,
    stdio: "inherit",
  });
  const buildCode = await new Promise((resolve) => build.on("exit", resolve));
  if (buildCode !== 0) {
    console.error("could not build imvault");
    return 1;
  }

  const server = spawn(join(dataDir, "imvault"), [], {
    cwd: root,
    env: {
      ...process.env,
      IMVAULT_DATA_DIR: dataDir,
      IMVAULT_ADDR: `127.0.0.1:${port}`,
      IMVAULT_BASE_URL: base,
      IMVAULT_LOG_LEVEL: "error",
    },
    stdio: ["ignore", "ignore", "inherit"],
  });

  const browser = spawn(
    browserPath,
    [
      "--headless=new",
      "--no-sandbox",
      "--disable-gpu",
      "--disable-dev-shm-usage",
      `--remote-debugging-port=${debugPort}`,
      "about:blank",
    ],
    { stdio: ["ignore", "ignore", "ignore"] },
  );

  let page = null;

  try {
    if (!(await waitForHttp(`${base}/healthz`))) throw new Error("the server did not start");

    // An administrator (the first account registered) plus a few accounts for
    // the filter and the delete check to work on.
    await seed(base, "boss", "boss@example.com", true);
    await seed(base, "alice", "alice@example.com");
    await seed(base, "bob", "bob@example.com");
    await seed(base, "carol", "carol@example.com");

    page = await Page.connect(debugPort);
    await signIn(page, base, "boss");

    // --- the admin page loads and wires itself up ---
    await page.goto(`${base}/admin/users`);

    record("Alpine initialises on the admin page", await page.evaluate(`
      return typeof window.Alpine !== "undefined";
    `));

    const rows = await page.evaluate(`
      return document.querySelectorAll("tr[data-search]").length;
    `);
    record("the account table rendered", rows === 4, `found ${rows} rows`);

    record("no JavaScript errors on load", !page.console.some((m) => m.level === "error"),
      page.console.filter((m) => m.level === "error").map((m) => m.text).join(" | "));

    // --- the client-side filter ---
    const filtered = await page.evaluate(`
      const input = document.querySelector(".filter-row input");
      input.value = "alice";
      input.dispatchEvent(new Event("input", { bubbles: true }));
      await new Promise((r) => setTimeout(r, 80));
      const visible = [...document.querySelectorAll("tr[data-search]")]
        .filter((row) => row.offsetParent !== null).length;
      return { visible, summary: document.querySelector(".filter-row .muted").textContent.trim() };
    `);
    record("the filter hides rows without a round trip", filtered.visible === 1,
      `${filtered.visible} visible, summary "${filtered.summary}"`);
    record("the filter reports how many are shown", filtered.summary === "1 of 4 shown",
      `summary was "${filtered.summary}"`);

    await page.evaluate(`
      const input = document.querySelector(".filter-row input");
      input.value = "";
      input.dispatchEvent(new Event("input", { bubbles: true }));
      await new Promise((r) => setTimeout(r, 80));
    `);

    // --- the confirmation dialog ---
    const opened = await page.evaluate(`
      const row = [...document.querySelectorAll("tr[data-search]")]
        .find((r) => r.dataset.search.includes("alice"));
      row.querySelector("button.danger").click();
      await new Promise((r) => setTimeout(r, 120));
      const dialog = document.querySelector("dialog.modal");
      return { open: dialog.open, text: dialog.textContent.replace(/\\s+/g, " ").trim() };
    `);
    record("clicking Delete opens the dialog", opened.open === true);
    record("the dialog names what is about to happen",
      opened.text.includes("Delete alice") && opened.text.includes("cannot be undone"),
      opened.text.slice(0, 90));
    record("no native confirm() was used", page.dialogs.length === 0,
      page.dialogs.join(" | "));

    // --- cancelling leaves everything alone ---
    const cancelled = await page.evaluate(`
      document.querySelector("dialog.modal .btn:not(.danger)").click();
      await new Promise((r) => setTimeout(r, 150));
      return {
        open: document.querySelector("dialog.modal").open,
        rows: document.querySelectorAll("tr[data-search]").length,
      };
    `);
    record("Cancel closes the dialog", cancelled.open === false);
    record("Cancel deletes nothing", cancelled.rows === 4, `${cancelled.rows} rows remain`);

    // --- confirming goes through htmx ---
    await page.evaluate(`
      const row = [...document.querySelectorAll("tr[data-search]")]
        .find((r) => r.dataset.search.includes("alice"));
      row.querySelector("button.danger").click();
      await new Promise((r) => setTimeout(r, 120));
      document.querySelector("dialog.modal .danger").click();
    `);

    let deleted = false;
    try {
      await page.waitFor(
        `![...document.querySelectorAll("tr[data-search]")].some((r) => r.dataset.search.includes("alice"))`,
        10000,
        "the deleted row to disappear",
      );
      deleted = true;
    } catch {
      deleted = false;
    }
    record("confirming removes the row via htmx", deleted);

    const notice = await page.evaluate(`
      const el = document.getElementById("admin-notice");
      return el ? el.textContent.replace(/\\s+/g, " ").trim() : "";
    `);
    record("the out-of-band notice reports it", notice.includes("Deleted alice"),
      notice.slice(0, 90));

    // --- the three-way visibility control ---
    //
    // Uploaded through the browser so the request carries the page's own CSRF
    // token, then driven the way a person would.
    const fileID = await page.evaluate(`
      const token = document.querySelector('meta[name="csrf-token"]').content;
      const form = new FormData();
      form.append("csrf_token", token);
      form.append("visibility", "members");
      const bytes = Uint8Array.from(atob(
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
      ), (c) => c.charCodeAt(0));
      form.append("files", new Blob([bytes], { type: "image/png" }), "pixel.png");
      const resp = await fetch("/upload", {
        method: "POST", body: form, headers: { "HX-Request": "true" },
      });
      const html = await resp.text();
      const match = html.match(/id="file-([A-Za-z0-9]+)"/);
      return match ? match[1] : "";
    `);
    record("a members-level upload succeeds", fileID !== "", "the upload returned no file card");

    await page.goto(`${base}/f/${fileID}`);

    const readPressed = `
      const pressed = document.querySelector(".vis-control button[aria-pressed='true']");
      return pressed ? pressed.textContent.trim() : "";
    `;
    const initial = await page.evaluate(readPressed);
    record("the control shows the stored level", initial === "Members", `showed "${initial}"`);

    await page.evaluate(`
      [...document.querySelectorAll(".vis-control button")]
        .find((b) => b.textContent.trim() === "Private").click();
      await new Promise((r) => setTimeout(r, 400));
    `);
    const swapped = await page.evaluate(readPressed);
    record("clicking a level swaps the control in place", swapped === "Private",
      `showed "${swapped}"`);

    // And it persisted, rather than only looking changed.
    await page.goto(`${base}/f/${fileID}`);
    const persisted = await page.evaluate(readPressed);
    record("the new level persisted", persisted === "Private", `showed "${persisted}"`);

    // --- a shared album ---
    await page.goto(`${base}/albums`);

    // Created by posting the form's own fields, rather than by clicking Submit:
    // a click navigates, which tears down the evaluation context mid-expression.
    // The field names and values are the form's, so this still exercises them.
    const created = await page.evaluate(`
      const token = document.querySelector('meta[name="csrf-token"]').content;
      const form = new FormData();
      form.append("csrf_token", token);
      form.append("title", "Browser Shared");
      form.append("visibility", "members");
      form.append("access", "members");
      const resp = await fetch("/albums", { method: "POST", body: form });
      return { ok: resp.ok, url: resp.url };
    `);
    record("a shared album can be created from the form's fields",
      created.ok && created.url.endsWith("/a/browser-shared"),
      JSON.stringify(created));

    await page.goto(`${base}/a/browser-shared`);

    // Upload one image to put in it, the way the uploader page does.
    const albumFile = await page.evaluate(`
      const token = document.querySelector('meta[name="csrf-token"]').content;
      const form = new FormData();
      form.append("csrf_token", token);
      form.append("visibility", "members");
      const bytes = Uint8Array.from(atob(
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
      ), (c) => c.charCodeAt(0));
      form.append("files", new Blob([bytes], { type: "image/png" }), "raid.png");
      const resp = await fetch("/upload", {
        method: "POST", body: form, headers: { "HX-Request": "true" },
      });
      const html = await resp.text();
      const match = html.match(/id="file-([A-Za-z0-9]+)"/);
      return match ? match[1] : "";
    `);
    record("the album's first image uploaded", albumFile !== "");

    await page.goto(`${base}/a/browser-shared`);

    // The owner gets a settings disclosure, which is where sharing is changed.
    const settings = await page.evaluate(`
      const details = document.querySelector("details.album-settings");
      if (!details) return { open: false, hasAccess: false };
      details.querySelector("summary").click();
      await new Promise((r) => setTimeout(r, 100));
      return {
        open: details.open,
        hasAccess: !!details.querySelector('select[name="access"]'),
        shared: details.querySelector('select[name="access"]').value,
      };
    `);
    record("the owner gets an album settings disclosure",
      settings.open && settings.hasAccess, JSON.stringify(settings));
    record("the disclosure shows the album is shared", settings.shared === "members",
      `access select said "${settings.shared}"`);

    // The add form offers the album's own images as candidates, and saving puts
    // the ticked one in.
    const candidate = await page.evaluate(`
      const form = document.getElementById("album-add");
      if (!form) return "";
      const box = form.querySelector('input[name="files"]');
      return box ? box.value : "";
    `);
    record("the add form offers the upload as a candidate", candidate === albumFile,
      `offered "${candidate}", expected "${albumFile}"`);

    const added = await page.evaluate(`
      const form = document.getElementById("album-add");
      const box = form.querySelector('input[name="files"]');
      const token = document.querySelector('meta[name="csrf-token"]').content;
      const body = new FormData();
      body.append("csrf_token", token);
      body.append("files", box.value);
      const resp = await fetch("/a/browser-shared/files", { method: "POST", body });
      return resp.ok;
    `);
    record("adding the ticked image is accepted", added === true);

    await page.goto(`${base}/a/browser-shared`);
    const inAlbum = await page.evaluate(`
      const grid = document.querySelector(".grid");
      const ids = grid ? [...grid.querySelectorAll(".card")].map((c) => c.id) : [];
      const form = document.getElementById("album-add");
      const stillOffered = form
        ? [...form.querySelectorAll('input[name="files"]')].some((b) => b.value === "${albumFile}")
        : false;
      return { ids, stillOffered };
    `);
    record("the image is in the album's own grid",
      inAlbum.ids.includes("file-" + albumFile), JSON.stringify(inAlbum.ids));
    record("and is no longer offered as a candidate", inAlbum.stillOffered === false,
      JSON.stringify(inAlbum));

    // The shared badge shows up in the album list.
    await page.goto(`${base}/albums`);
    record("a shared album is badged as shared", await page.evaluate(`
      const cards = [...document.querySelectorAll(".album-card")];
      const card = cards.find((c) => c.textContent.includes("Browser Shared"));
      return !!card && card.textContent.includes("shared");
    `));
    // --- the guard on deleting your own account ---
    await page.goto(`${base}/settings/account`);

    const guard = await page.evaluate(`
      const form = document.querySelector('form[action="/settings/account/delete"]');
      if (!form) return { error: "the account page has no delete form" };

      // The password has to be filled in, or the browser's own required-field
      // validation blocks the submission and no submit event is ever fired,
      // which would make this check pass without testing anything.
      const password = form.querySelector('input[name="password"]');
      password.value = "hunter2hunter2";

      // Typing the wrong name must stop the submission before it leaves.
      const typed = form.querySelector('input[name="confirm"]');
      typed.value = "not-my-name";
      typed.dispatchEvent(new Event("input", { bubbles: true }));
      await new Promise((r) => setTimeout(r, 60));

      let prevented = null;
      form.addEventListener("submit", (e) => { prevented = e.defaultPrevented; });
      form.querySelector('button[type="submit"]').click();
      await new Promise((r) => setTimeout(r, 200));

      return { prevented, url: location.pathname };
    `);

    record("a mistyped username blocks account deletion", guard.prevented === true,
      JSON.stringify(guard));
    record("and the page does not navigate", guard.url === "/settings/account",
      `landed on ${guard.url}`);

    // The account is still there, which is the outcome that matters.
    await page.goto(`${base}/settings/account`);
    const stillThere = await page.evaluate(`
      return !!document.querySelector('form[action="/settings/account/delete"]');
    `);
    record("the account survived the blocked attempt", stillThere === true);

    record("no JavaScript errors during the interactions",
      !page.console.some((m) => m.level === "error"),
      page.console.filter((m) => m.level === "error").map((m) => m.text).join(" | "));
  } finally {
    if (page) page.socket.close();
    browser.kill("SIGKILL");
    server.kill("SIGTERM");
    await sleep(300);
    await rm(dataDir, { recursive: true, force: true });
  }

  console.log("");
  for (const { name, ok, detail } of results) {
    console.log(`  ${ok ? "PASS" : "FAIL"} ${name}${detail && !ok ? ` — ${detail}` : ""}`);
  }
  console.log(`\n${results.length - failures} passed, ${failures} failed`);
  return failures === 0 ? 0 : 1;
}

// seed registers an account over HTTP, the way the browser would.
async function seed(base, username, email, admin = false) {
  const jar = new Map();

  const request = async (path, options = {}) => {
    const headers = new Headers(options.headers || {});
    if (jar.size) {
      headers.set("Cookie", [...jar].map(([k, v]) => `${k}=${v}`).join("; "));
    }
    const res = await fetch(base + path, { ...options, headers, redirect: "manual" });

    for (const cookie of res.headers.getSetCookie?.() || []) {
      const [pair] = cookie.split(";");
      const index = pair.indexOf("=");
      jar.set(pair.slice(0, index), pair.slice(index + 1));
    }
    return res;
  };

  await request("/");
  const csrf = jar.get("imvault_csrf");

  const body = new URLSearchParams({
    csrf_token: csrf,
    username,
    password: "hunter2hunter2",
    email,
  });
  const res = await request("/register", {
    method: "POST",
    body,
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
  });
  if (res.status !== 303) {
    throw new Error(`seeding ${username} failed with ${res.status}`);
  }
  return jar;
}

// signIn logs the browser in by posting the form from the page, so the session
// cookie lands in the browser's own jar rather than being injected.
async function signIn(page, base, username) {
  await page.goto(`${base}/login`);
  const problem = await page.evaluate(`
    const match = document.cookie.match(/imvault_csrf=([^;]+)/);
    if (!match) return "no csrf cookie";
    const body = new URLSearchParams({
      csrf_token: match[1],
      username: ${JSON.stringify(username)},
      password: "hunter2hunter2",
    });
    // The status is unreadable here: redirect "manual" gives an opaque response
    // in a browser, where the spec reports 0. The Set-Cookie still lands, so
    // the check is made by where the next navigation ends up.
    await fetch("/login", {
      method: "POST",
      body,
      redirect: "manual",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
    });
    return "";
  `);
  if (problem) throw new Error(problem);

  await page.goto(`${base}/admin/users`);
  const signedIn = await page.evaluate(`
    return !document.querySelector('form[action="/login"]')
      && !!document.querySelector(".filter-row input");
  `);
  if (!signedIn) throw new Error("sign in did not take: still on the login page");
}

process.exit(await main());
