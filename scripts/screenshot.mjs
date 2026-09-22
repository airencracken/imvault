#!/usr/bin/env node
// SPDX-License-Identifier: AGPL-3.0-or-later
// Capture the real demo UI. Start `make demo` first; no remote assets are used.

import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { Page, findBrowser } from "./browser-check.mjs";

const base = new URL(process.argv[2] || "http://127.0.0.1:8080");
if (!["127.0.0.1", "localhost", "[::1]"].includes(base.hostname)) {
	throw new Error("Capture a local demo instance, not a live deployment.");
}
const output = process.argv[3] || "docs/images/recent.png";
const browserPath = await findBrowser();
if (!browserPath) throw new Error("A Chromium-family browser is required.");
const profile = await mkdtemp(join(tmpdir(), "imvault-screenshot-"));
const browser = spawn(browserPath, [
	"--headless=new", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage",
	`--user-data-dir=${profile}`, "--remote-debugging-port=0", "about:blank",
], { stdio: "ignore" });
const browserExited = once(browser, "exit");
let page;

try {
	let port;
	for (let attempt = 0; attempt < 100; attempt++) {
		try {
			port = Number((await readFile(join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]);
			break;
		} catch {
			await new Promise(resolve => setTimeout(resolve, 100));
		}
	}
	if (!port) throw new Error("The browser did not start.");
	page = await Page.connect(port);
	await page.send("Emulation.setDeviceMetricsOverride", {
		width: 1120, height: 900, deviceScaleFactor: 1, mobile: false,
	});
	await page.goto(new URL("/login", base).href);
	await page.evaluate(`
		const token = document.querySelector('meta[name="csrf-token"]').content;
		const body = new URLSearchParams({csrf_token: token,
			username: ${JSON.stringify(process.env.SCREENSHOT_USERNAME || "demo")},
			password: ${JSON.stringify(process.env.SCREENSHOT_PASSWORD || "demo-password")}});
		const result = await fetch('/login', {method: 'POST', body});
		if (!result.ok) throw new Error('Demo sign-in failed: ' + result.status);
	`);
	await page.goto(new URL("/recent", base).href);
	await page.waitFor("document.querySelectorAll('.grid .card').length > 0", 10000, "seeded media");
	await page.evaluate(`
		await document.fonts.ready;
		await Promise.all([...document.images].map(img => img.decode().catch(() => {})));
	`);
	const { data } = await page.send("Page.captureScreenshot", { format: "png" });
	await mkdir(dirname(output), { recursive: true });
	await writeFile(output, Buffer.from(data, "base64"));
	console.log(`Saved ${output}`);
} finally {
	page?.socket.close();
	browser.kill("SIGKILL");
	await browserExited;
	await rm(profile, { recursive: true, force: true });
}
