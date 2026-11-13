// SPDX-License-Identifier: AGPL-3.0-or-later

// Tests for the client-side logic in app.js.
//
// Run with: node --test internal/web/static/js/
//
// app.js is a plain script loaded from a <script> tag, so there is nothing to
// import. It is executed here inside a VM with a minimal document shim, so the
// handlers can be exercised directly without any dependency: node --test is
// built in.

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const SOURCE = fs.readFileSync(path.join(__dirname, "app.js"), "utf8");

function loadBulkSelection() {
  const elements = {};
  for (const id of ["bulk-tags", "bulk-count", "bulk-submit", "bulk-album", "bulk-visibility", "bulk-selection", "bulk-select-all", "bulk-clear", "bulk-album-select", "bulk-album-create", "bulk-album-title", "bulk-album-error", "bulk-panel"]) {
    elements[id] = { listeners: {}, addEventListener(name, fn) { this.listeners[name] = fn; } };
  }
  elements["bulk-album-select"].value = "";
  elements["bulk-album-title"].value = "";
  elements["bulk-album-title"].focus = function () { this.focused = true; };
  let picks = [{ checked: false }, { checked: false }];
  const listeners = {};
  const bodyListeners = {};
  const windowListeners = {};
  const document = {
    getElementById: (id) => elements[id] || null,
    querySelectorAll(selector) {
      assert.equal(selector, 'input[name="files"][form="bulk-tags"]');
      return picks;
    },
    addEventListener: (name, fn) => { listeners[name] = fn; },
    body: { addEventListener: (name, fn) => { bodyListeners[name] = fn; } },
  };
  vm.runInNewContext(SOURCE, { document, window: { addEventListener: (name, fn) => { windowListeners[name] = fn; } } });
  return { elements, picks, listeners, bodyListeners, windowListeners, remove: () => { picks = []; } };
}

test("bulk selection counts individual picks, selects the page and clears it", () => {
  const { elements, picks, listeners } = loadBulkSelection();
  assert.equal(elements["bulk-selection"].hidden, false);
  assert.equal(elements["bulk-submit"].disabled, true);
  assert.equal(elements["bulk-album"].disabled, true);
  assert.equal(elements["bulk-visibility"].disabled, true);
  assert.equal(elements["bulk-count"].textContent, "0 selected");
  assert.equal(elements["bulk-panel"].hidden, true, "the organise panel waits for a selection");
  picks[0].checked = true;
  listeners.change();
  assert.equal(elements["bulk-count"].textContent, "1 selected");
  assert.equal(elements["bulk-panel"].hidden, false);
  assert.equal(elements["bulk-panel"].open, true, "the first pick opens the panel");
  elements["bulk-panel"].open = false;
  picks[1].checked = true;
  listeners.change();
  assert.equal(elements["bulk-panel"].open, false, "a panel closed by hand stays closed");
  picks[1].checked = false;
  assert.equal(elements["bulk-submit"].disabled, false);
  assert.equal(elements["bulk-album"].disabled, false);
  assert.equal(elements["bulk-visibility"].disabled, false);
  elements["bulk-select-all"].listeners.click();
  assert.ok(picks.every((pick) => pick.checked));
  assert.equal(elements["bulk-count"].textContent, "2 selected");
  elements["bulk-clear"].listeners.click();
  assert.ok(picks.every((pick) => !pick.checked));
  assert.equal(elements["bulk-submit"].disabled, true);
});

test("bulk selection handles browser restore, deleted cards and empty submission", () => {
  const { elements, picks, windowListeners, bodyListeners, remove } = loadBulkSelection();
  picks[0].checked = true;
  windowListeners.pageshow();
  assert.equal(elements["bulk-count"].textContent, "1 selected");
  let prevented = false;
  const event = { preventDefault() { prevented = true; } };
  elements["bulk-tags"].listeners.submit(event);
  assert.equal(prevented, false);
  remove();
  bodyListeners["htmx:afterSwap"]();
  assert.equal(elements["bulk-count"].textContent, "0 selected");
  elements["bulk-tags"].listeners.submit(event);
  assert.equal(prevented, true);
});

test("bulk album creation reveals its title and keeps selection when validation fails", () => {
  const { elements, picks, listeners, windowListeners } = loadBulkSelection();
  const title = elements["bulk-album-title"];
  assert.equal(elements["bulk-album-create"].hidden, true);
  assert.equal(title.disabled, true);
  picks[0].checked = true;
  elements["bulk-album-select"].value = "new";
  listeners.change();
  assert.equal(elements["bulk-album-create"].hidden, false);
  assert.equal(title.disabled, false);
  title.value = "   ";
  let prevented = false;
  const event = { submitter: {id: "bulk-album"}, preventDefault() { prevented = true; } };
  elements["bulk-tags"].listeners.submit(event);
  assert.equal(prevented, true);
  assert.equal(elements["bulk-album-error"].hidden, false);
  assert.equal(title.focused, true);
  assert.equal(picks[0].checked, true);
  // The album title never blocks unrelated tags or visibility actions.
  for (const action of ["bulk-submit", "bulk-visibility"]) {
    prevented = false;
    elements["bulk-tags"].listeners.submit({...event, submitter: {id: action}});
    assert.equal(prevented, false);
  }
  title.value = "Family picnic";
  title.listeners.input();
  assert.equal(elements["bulk-album-error"].hidden, true);
  prevented = false;
  elements["bulk-tags"].listeners.submit(event);
  assert.equal(prevented, false);
  elements["bulk-album-select"].value = "42";
  windowListeners.pageshow();
  assert.equal(elements["bulk-album-create"].hidden, true);
  assert.equal(title.disabled, true);
  assert.equal(title.value, "Family picnic", "Switching albums keeps the unfinished title");
});

// loadBehaviours runs app.js against a shim with the confirmation dialog and
// returns the helpers it exposes on window.imvault.
function loadBehaviours(extra) {
  const elements = {};
  const make = (props) => Object.assign({ hidden: false, textContent: "", listeners: {}, addEventListener(name, fn) { this.listeners[name] = fn; } }, props);
  const accept = make({});
  const dialog = make({
    open: false,
    showModal() { this.open = true; },
    close() { this.open = false; },
    querySelector: (selector) => (selector === "[data-confirm-accept]" ? accept : null),
  });
  elements["confirm-dialog"] = dialog;
  elements["confirm-message"] = make({});
  elements["confirm-detail"] = make({});
  Object.assign(elements, extra || {});
  const listeners = {};
  const document = {
    getElementById: (id) => elements[id] || null,
    addEventListener: (name, fn) => { listeners[name] = fn; },
    body: { addEventListener() {} },
  };
  const window = { addEventListener() {} };
  vm.runInNewContext(SOURCE, { document, window, setTimeout, navigator: {} });
  return { api: window.imvault, listeners, elements, dialog, accept };
}

function attrs(map) {
  return (name) => (Object.prototype.hasOwnProperty.call(map, name) ? map[name] : null);
}

function submitEvent(form, submitter) {
  return {
    target: form,
    submitter: submitter || null,
    prevented: false,
    stopped: false,
    preventDefault() { this.prevented = true; },
    stopPropagation() { this.stopped = true; },
  };
}

test("behaviours are registered on the document, with submit in the capture phase", () => {
  const { api, listeners } = loadBehaviours();
  for (const name of ["submit", "click", "focusin", "input", "DOMContentLoaded"]) {
    assert.equal(typeof listeners[name], "function", `expected a ${name} listener`);
  }
  for (const name of ["matches", "summary", "onSubmit", "onClick", "settle"]) {
    assert.equal(typeof api[name], "function");
  }
});

test("a data-confirm form opens the dialog instead of submitting", () => {
  const { api, dialog, accept, elements } = loadBehaviours();
  let submitted = [];
  const form = {
    dataset: {},
    getAttribute: attrs({ "data-confirm": "Delete this album?", "data-confirm-detail": "The files in it are kept.", "data-confirm-action": "Delete album" }),
    requestSubmit: (button) => submitted.push(button || null),
  };
  const first = submitEvent(form);
  api.onSubmit(first);
  assert.equal(first.prevented, true);
  assert.equal(first.stopped, true, "htmx must not see the unconfirmed submit");
  assert.equal(dialog.open, true);
  assert.equal(elements["confirm-message"].textContent, "Delete this album?");
  assert.equal(elements["confirm-detail"].textContent, "The files in it are kept.");
  assert.equal(elements["confirm-detail"].hidden, false);
  assert.equal(accept.textContent, "Delete album");

  // Cancelling submits nothing.
  api.settle(false);
  assert.equal(dialog.open, false);
  assert.deepEqual(submitted, []);

  // Accepting resubmits once, and the resubmission passes straight through.
  api.onSubmit(submitEvent(form));
  api.settle(true);
  assert.equal(submitted.length, 1);
  assert.equal(form.dataset.confirmed, "1");
  const second = submitEvent(form);
  api.onSubmit(second);
  assert.equal(second.prevented, false);
  assert.equal(form.dataset.confirmed, undefined, "the mark is used once");

  // Accepting with nothing pending is harmless.
  api.settle(true);
  assert.equal(submitted.length, 1);
});

test("a button's data-confirm wins and is resubmitted as the submitter", () => {
  const { api, elements, accept } = loadBehaviours();
  const submitted = [];
  const form = { dataset: {}, getAttribute: attrs({}), requestSubmit: (button) => submitted.push(button) };
  const button = { getAttribute: attrs({ "data-confirm": "Disable alice?" }) };
  api.onSubmit(submitEvent(form, button));
  assert.equal(elements["confirm-message"].textContent, "Disable alice?");
  assert.equal(elements["confirm-detail"].hidden, true, "no detail means no empty line");
  assert.equal(accept.textContent, "Confirm", "the action defaults");
  api.settle(true);
  assert.deepEqual(submitted, [button]);

  // A form with no question is left alone.
  const plain = submitEvent({ dataset: {}, getAttribute: attrs({}) });
  api.onSubmit(plain);
  assert.equal(plain.prevented, false);
});

test("hostile confirmation text is shown as text, never markup", () => {
  const { api, elements } = loadBehaviours();
  const form = { dataset: {}, getAttribute: attrs({ "data-confirm": "<img src=x onerror=alert(1)>" }), requestSubmit() {} };
  api.onSubmit(submitEvent(form));
  assert.equal(elements["confirm-message"].textContent, "<img src=x onerror=alert(1)>");
  assert.equal(elements["confirm-message"].innerHTML, undefined);
});

test("without dialog support the form submits normally", () => {
  const { api, dialog } = loadBehaviours();
  delete dialog.showModal;
  const event = submitEvent({ dataset: {}, getAttribute: attrs({ "data-confirm": "Sure?" }) });
  api.onSubmit(event);
  assert.equal(event.prevented, false);
});

test("typing a name to confirm blocks the submit until it matches", () => {
  const { api } = loadBehaviours();
  const typed = { value: "alic", focus() { this.focused = true; } };
  const form = { dataset: {}, getAttribute: attrs({ "data-type-to-confirm": "alice" }), querySelector: () => typed };
  const blocked = submitEvent(form);
  api.onSubmit(blocked);
  assert.equal(blocked.prevented, true);
  assert.equal(typed.focused, true);
  typed.value = "alice";
  const allowed = submitEvent(form);
  api.onSubmit(allowed);
  assert.equal(allowed.prevented, false);
  // A name that looks like markup is compared as plain text.
  form.getAttribute = attrs({ "data-type-to-confirm": "a\"><b>" });
  typed.value = "a\"><b>";
  const odd = submitEvent(form);
  api.onSubmit(odd);
  assert.equal(odd.prevented, false);
});

test("table filtering matches search text case-insensitively and summarises", () => {
  const { api } = loadBehaviours();
  const alice = { dataset: { search: "alice alice@example.com" } };
  const bob = { dataset: { search: "bob bob@example.com" } };
  assert.equal(api.matches(alice, ""), true);
  assert.equal(api.matches(alice, "ALICE"), true);
  assert.equal(api.matches(bob, "ALICE"), false);
  assert.equal(api.matches(bob, "  bob  "), true);
  assert.equal(api.matches({ dataset: {} }, "anything"), false, "a missing attribute must not throw");
  const rows = [alice, bob, { dataset: { search: "carol carol@example.com" } }];
  assert.equal(api.summary(rows, ""), "3 total");
  assert.equal(api.summary(rows, "bob"), "1 of 3 shown");
  assert.equal(api.summary(rows, "example.com"), "3 of 3 shown");
  assert.equal(api.summary(null, "x"), "");
});

test("typing in a filter box hides rows in the named table", () => {
  const rows = [
    { dataset: { search: "alice" }, hidden: false },
    { dataset: { search: "bob" }, hidden: false },
  ];
  const table = { querySelectorAll: (selector) => { assert.equal(selector, "tr[data-search]"); return rows; } };
  const label = { textContent: "" };
  const { listeners } = loadBehaviours({ "users-table": table, "users-summary": label });
  const input = { value: "bo", getAttribute: attrs({ "data-table-filter": "#users-table", "data-table-summary": "#users-summary" }) };
  listeners.input({ target: input });
  assert.deepEqual(rows.map((r) => r.hidden), [true, false]);
  assert.equal(label.textContent, "1 of 2 shown");
  // A selector that is not an id is ignored rather than queried.
  listeners.input({ target: { value: "x", getAttribute: attrs({ "data-table-filter": "body *" }) } });
});

test("read-only links select themselves on focus", () => {
  const { api } = loadBehaviours();
  let selected = false;
  api.onFocus({ target: { hasAttribute: (name) => name === "data-select-on-focus", select() { selected = true; } } });
  assert.equal(selected, true);
  selected = false;
  api.onFocus({ target: { hasAttribute: () => false, select() { selected = true; } } });
  assert.equal(selected, false);
});

test("choosing public on the uploader turns camera details and location off", () => {
  const { api } = loadBehaviours();
  const boxes = [{ checked: true }, { checked: true }];
  const form = { querySelectorAll: (selector) => { assert.equal(selector, "input[data-off-when-public]"); return boxes; } };
  const radio = (value) => ({ name: "visibility", type: "radio", value, form, closest: () => null });
  api.onClick({ target: radio("public") });
  assert.deepEqual(boxes.map((b) => b.checked), [false, false]);
  api.onClick({ target: radio("members") });
  assert.deepEqual(boxes.map((b) => b.checked), [false, false]);
  boxes[0].checked = true;
  api.onClick({ target: radio("private") });
  assert.deepEqual(boxes.map((b) => b.checked), [true, false]);
});

// The body-level listeners are registered against document.body, so they are
// captured by running the script against a shim that records them.
function loadBodyListeners() {
  const listeners = new Map();
  const document = {
    getElementById: () => null,
    addEventListener: () => {},
    body: { addEventListener: (name, handler) => listeners.set(name, handler) },
  };

  const context = {
    document,
    window: {},
    setTimeout,
    console,
  };
  vm.runInNewContext(SOURCE, context);
  return listeners;
}

test("a 503 from htmx is shown rather than swallowed", () => {
  const handler = loadBodyListeners().get("htmx:beforeSwap");
  assert.ok(handler, "expected an htmx:beforeSwap listener");

  // htmx's default is to leave the page alone on a 5xx, which is right for a
  // crash. A busy server sends a fragment meant to be read, so that one has to
  // be swapped or the uploader page silently does nothing.
  const busy = { detail: { xhr: { status: 503 }, shouldSwap: false, isError: true } };
  handler(busy);
  assert.equal(busy.detail.shouldSwap, true);
  assert.equal(busy.detail.isError, false);

  // Every other status is left exactly as htmx decided.
  const crash = { detail: { xhr: { status: 500 }, shouldSwap: false, isError: true } };
  handler(crash);
  assert.equal(crash.detail.shouldSwap, false);
  assert.equal(crash.detail.isError, true);

  const fine = { detail: { xhr: { status: 200 }, shouldSwap: true, isError: false } };
  handler(fine);
  assert.equal(fine.detail.shouldSwap, true);

  // A detail without an xhr must not throw: the listener runs for every swap.
  handler({ detail: {} });
});

// loadUploader runs app.js against a shim of the upload page: the file input
// with the real accept list, the dropzone, the count line and the form.
function loadUploader() {
  const elements = {};
  const listen = () => ({ listeners: {}, addEventListener(name, fn) { this.listeners[name] = fn; } });
  elements["file-input"] = Object.assign(listen(), {
    files: [{ name: "kept.png", type: "image/png" }],
    getAttribute: (name) => (name === "accept" ? "image/*,video/webm,video/mp4,video/quicktime" : null),
  });
  elements.dropzone = Object.assign(listen(), { classList: { add() {}, remove() {} } });
  elements["file-count"] = { textContent: "", title: "" };
  elements["upload-form"] = listen();
  const documentListeners = {};
  const triggered = [];
  class DataTransfer {
    constructor() {
      const files = [];
      this.files = files;
      this.items = { add: (file) => files.push(file) };
    }
  }
  const document = {
    getElementById: (id) => elements[id] || null,
    addEventListener: (name, fn) => { documentListeners[name] = fn; },
    body: { addEventListener() {} },
  };
  const window = { htmx: { trigger: (el, name) => triggered.push(name) }, addEventListener() {} };
  vm.runInNewContext(SOURCE, { document, window, DataTransfer });
  return { elements, documentListeners, triggered };
}

function drop(elements, files) {
  const event = { preventDefault() {}, dataTransfer: { files } };
  elements.dropzone.listeners.drop(event);
}

test("dropping clips stages them alongside images", () => {
  const { elements } = loadUploader();
  drop(elements, [
    { name: "clip.mp4", type: "video/mp4" },
    { name: "clip.webm", type: "video/webm" },
    { name: "clip.mov", type: "video/quicktime" },
    { name: "photo.jpg", type: "image/jpeg" },
    { name: "notes.txt", type: "text/plain" },
    { name: "mystery", type: "" },
  ]);
  const names = elements["file-input"].files.map((f) => f.name);
  assert.deepEqual(names, ["clip.mp4", "clip.webm", "clip.mov", "photo.jpg"]);
  assert.equal(elements["file-count"].textContent, "4 files selected");
});

test("dropping nothing usable keeps the current selection", () => {
  const { elements } = loadUploader();
  drop(elements, [{ name: "notes.txt", type: "text/plain" }, { name: "x.avi", type: "video/x-msvideo" }]);
  assert.deepEqual(elements["file-input"].files.map((f) => f.name), ["kept.png"]);
  assert.match(elements["file-count"].textContent, /None of those files/);
});

test("pasting a clip uploads it, and pasting nothing usable does not submit", () => {
  const { elements, documentListeners, triggered } = loadUploader();
  const paste = (files) => documentListeners.paste({
    target: { tagName: "BODY" },
    preventDefault() {},
    clipboardData: { items: files.map((file) => ({ kind: "file", getAsFile: () => file })) },
  });
  paste([{ name: "clip.mp4", type: "video/mp4" }]);
  assert.deepEqual(triggered, ["submit"]);
  assert.deepEqual(elements["file-input"].files.map((f) => f.name), ["clip.mp4"]);
  paste([{ name: "notes.txt", type: "text/plain" }]);
  assert.deepEqual(triggered, ["submit"], "an unusable paste submitted the form");
});
