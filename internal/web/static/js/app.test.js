// SPDX-License-Identifier: AGPL-3.0-or-later

// Tests for the client-side logic in app.js.
//
// Run with: node --test internal/web/static/js/
//
// app.js is a plain script loaded from a <script> tag, so there is nothing to
// import. It is executed here inside a VM with a minimal document shim, which
// lets the Alpine registrations be captured and exercised directly. That keeps
// this test free of any dependency: node --test is built in.

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const SOURCE = fs.readFileSync(path.join(__dirname, "app.js"), "utf8");

// The Alpine object the listener uses is the global one, so it is captured by
// running the listener against a shim that records the calls.
function loadAlpineRegistrations() {
  const listeners = new Map();
  const document = {
    getElementById: () => null,
    addEventListener: (name, handler) => listeners.set(name, handler),
    body: { addEventListener: () => {} },
  };

  const stores = {};
  const data = {};
  const Alpine = {
    store: (name, value) => {
      stores[name] = value;
    },
    data: (name, factory) => {
      data[name] = factory;
    },
  };

  const context = { document, window: {}, setTimeout, console, Alpine };
  vm.runInNewContext(SOURCE, context);
  listeners.get("alpine:init")();

  return { stores, data };
}

test("registers the confirm store and the table filter component", () => {
  const { stores, data } = loadAlpineRegistrations();

  assert.ok(stores.confirm, "expected a confirm store");
  assert.ok(data.tableFilter, "expected a tableFilter component");

  const confirm = stores.confirm;
  for (const method of ["ask", "cancel", "accept"]) {
    assert.equal(typeof confirm[method], "function", `confirm.${method} should be a function`);
  }
  assert.equal(confirm.open, false, "the dialog should start closed");
});

test("confirm ask/cancel/accept drives the dialog and submits the form", () => {
  const { stores } = loadAlpineRegistrations();
  const confirm = stores.confirm;

  let submitted = 0;
  const form = { dataset: {}, requestSubmit: () => submitted++ };

  confirm.ask(form, {
    message: "Delete alice?",
    detail: "This cannot be undone.",
    action: "Delete account",
  });

  assert.equal(confirm.open, true);
  assert.equal(confirm.message, "Delete alice?");
  assert.equal(confirm.detail, "This cannot be undone.");
  assert.equal(confirm.action, "Delete account");
  assert.equal(confirm.form, form);
  assert.equal(submitted, 0, "asking should not submit anything");

  // Cancelling leaves the form alone and clears the pending target.
  confirm.cancel();
  assert.equal(confirm.open, false);
  assert.equal(confirm.form, null);
  assert.equal(submitted, 0);

  // Accepting submits exactly the form it was given, once, and marks it so the
  // click interceptor lets the resubmission through.
  confirm.ask(form, { message: "Delete alice?" });
  confirm.accept();
  assert.equal(submitted, 1, "accepting should submit the form once");
  assert.equal(form.dataset.confirmed, "1", "the form should be marked as confirmed");
  assert.equal(confirm.open, false, "the dialog should close after accepting");
  assert.equal(confirm.form, null, "the pending form should be cleared");
});

test("confirm defaults its optional wording", () => {
  const { stores } = loadAlpineRegistrations();
  const confirm = stores.confirm;

  confirm.ask({ dataset: {}, requestSubmit() {} }, { message: "Sure?" });

  // A trigger that omits them should still produce a usable dialog.
  assert.equal(confirm.detail, "");
  assert.equal(confirm.action, "Confirm");
});

test("tableFilter matches on the row's search text, case insensitively", () => {
  const { data } = loadAlpineRegistrations();
  const filter = data.tableFilter();

  const alice = { dataset: { search: "alice alice@example.com" } };
  const bob = { dataset: { search: "bob bob@example.com" } };

  // An empty query shows everything.
  assert.equal(filter.matches(alice), true);
  assert.equal(filter.matches(bob), true);

  filter.q = "ALICE";
  assert.equal(filter.matches(alice), true, "matching should ignore case");
  assert.equal(filter.matches(bob), false);

  filter.q = "example.com";
  assert.equal(filter.matches(alice), true, "the address should be searchable too");

  filter.q = "  bob  ";
  assert.equal(filter.matches(bob), true, "surrounding whitespace should be ignored");
  assert.equal(filter.matches(alice), false);

  filter.q = "nobody";
  assert.equal(filter.matches(alice), false);
  assert.equal(filter.matches(bob), false);
});

test("tableFilter tolerates a row with no search text", () => {
  const { data } = loadAlpineRegistrations();
  const filter = data.tableFilter();

  assert.equal(filter.matches({ dataset: {} }), true, "an empty query matches");

  filter.q = "anything";
  assert.equal(filter.matches({ dataset: {} }), false, "a missing attribute must not throw");
});

test("tableFilter summarises how many rows are shown", () => {
  const { data } = loadAlpineRegistrations();
  const filter = data.tableFilter();

  const rows = [
    { dataset: { search: "alice alice@example.com" } },
    { dataset: { search: "bob bob@example.com" } },
    { dataset: { search: "carol carol@example.com" } },
  ];
  const table = { querySelectorAll: () => rows };

  assert.equal(filter.summary(table), "3 total");

  filter.q = "bob";
  assert.equal(filter.summary(table), "1 of 3 shown");

  filter.q = "example.com";
  assert.equal(filter.summary(table), "3 of 3 shown");

  // A table that is not on the page yet must not throw.
  assert.equal(filter.summary(null), "");
});

test("accepting without a pending form is harmless", () => {
  const { stores } = loadAlpineRegistrations();
  const confirm = stores.confirm;

  confirm.accept();
  assert.equal(confirm.open, false);
  assert.equal(confirm.form, null);
});
