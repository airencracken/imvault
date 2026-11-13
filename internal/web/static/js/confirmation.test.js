const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

test("a queued close event preserves a newly opened confirmation", () => {
  const listeners = {};
  const queued = [];
  const box = {
    open: false,
    showModal() { this.open = true; },
    close() { this.open = false; queued.push(() => this.onClose()); },
    addEventListener(name, fn) { if (name === "close") this.onClose = fn; },
    querySelector() { return {}; },
  };
  const elements = { "confirm-dialog": box, "confirm-message": {}, "confirm-detail": {} };
  const document = {
    getElementById: id => elements[id] || null,
    querySelectorAll: () => [],
    addEventListener(name, fn) { listeners[name] = fn; },
    body: { addEventListener() {} },
  };
  const window = {};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, "app.js"), "utf8"), { document, window });
  listeners.DOMContentLoaded();
  let submitted = 0;
  const form = {
    dataset: {},
    getAttribute: name => name === "data-confirm" ? "Revoke this invitation?" : null,
    requestSubmit() { submitted++; },
  };
  const open = () => window.imvault.onSubmit({ target: form, preventDefault() {}, stopPropagation() {} });
  open();
  window.imvault.settle(false);
  open();
  // HTMLDialogElement.close queues its close event; it may arrive after
  // another submit has already reopened this same dialog.
  queued.shift()();
  assert.equal(box.open, true);
  window.imvault.settle(true);
  assert.equal(submitted, 1, "Confirm must submit the currently open action");
});
