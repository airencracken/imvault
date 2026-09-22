const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = readFileSync(join(__dirname, 'theme.js'), 'utf8');
const key = 'imvault-theme';

function page(saved, blocked = false) {
  const documentEvents = {};
  const windowEvents = {};
  const attributes = new Map();
  const storage = new Map(saved === undefined ? [] : [[key, saved]]);
  let controls = [];
  const document = {
    documentElement: {
      setAttribute: (name, value) => attributes.set(name, value),
      removeAttribute: name => attributes.delete(name),
    },
    querySelectorAll: () => controls,
    addEventListener: (name, handler) => { documentEvents[name] = handler; },
  };
  vm.runInNewContext(source, {
    document,
    window: { addEventListener: (name, handler) => { windowEvents[name] = handler; } },
    localStorage: {
      getItem(name) { if (blocked) throw new Error('Disabled'); return storage.get(name); },
      setItem(name, value) { if (blocked) throw new Error('Disabled'); storage.set(name, value); },
      removeItem(name) { if (blocked) throw new Error('Disabled'); storage.delete(name); },
    },
  });
  return {
    attributes, storage,
    mount(event = 'DOMContentLoaded') {
      const label = { hidden: true };
      const control = { value: 'system', closest: () => label, matches: selector => selector === '[data-theme-select]' };
      controls = [control];
      documentEvents[event]();
      return { label, control };
    },
    change(control, value) { control.value = value; documentEvents.change({ target: control }); },
    storageEvent: event => windowEvents.storage(event),
    restore: () => windowEvents.pageshow({ persisted: true }),
  };
}

test('saved themes apply before the body exists and initialize the accessible selector', () => {
  for (const theme of ['light', 'dark']) {
    const tab = page(theme);
    assert.equal(tab.attributes.get('data-theme'), theme);
    const { control, label } = tab.mount();
    assert.equal(control.value, theme);
    assert.equal(label.hidden, false);
  }
});

test('missing or invalid saved choices follow the system', () => {
  for (const saved of [undefined, '', 'system', 'purple', '<script>']) {
    const tab = page(saved);
    assert.equal(tab.attributes.has('data-theme'), false);
    assert.equal(tab.mount().control.value, 'system');
  }
});

test('switching themes persists the choice and system clears the override', () => {
  const tab = page();
  const { control } = tab.mount();
  for (const theme of ['dark', 'light']) {
    tab.change(control, theme);
    assert.equal(tab.attributes.get('data-theme'), theme);
    assert.equal(tab.storage.get(key), theme);
  }
  tab.change(control, 'system');
  assert.equal(tab.attributes.has('data-theme'), false);
  assert.equal(tab.storage.has(key), false);
});

test('unrelated form changes cannot change the theme', () => {
  const tab = page('dark');
  tab.change({ matches: () => false }, 'light');
  assert.equal(tab.attributes.get('data-theme'), 'dark');
  assert.equal(tab.storage.get(key), 'dark');
});

test('the switch and swapped controls work when browser storage is blocked', () => {
  const tab = page(undefined, true);
  const { control } = tab.mount();
  tab.change(control, 'dark');
  assert.equal(tab.attributes.get('data-theme'), 'dark');
  const swapped = tab.mount('htmx:afterSwap');
  assert.equal(swapped.control.value, 'dark');
  assert.equal(swapped.label.hidden, false);
  tab.change(swapped.control, 'system');
  assert.equal(tab.attributes.has('data-theme'), false);
});

test('other tabs can change or clear the preference; unrelated keys are ignored', () => {
  const tab = page('dark');
  const { control } = tab.mount();
  tab.storageEvent({ key: 'unrelated', newValue: 'light' });
  assert.equal(control.value, 'dark');
  tab.storageEvent({ key, newValue: 'light' });
  assert.equal(control.value, 'light');
  assert.equal(tab.attributes.get('data-theme'), 'light');
  for (const event of [{ key, newValue: null }, { key: null, newValue: null }]) {
    tab.storageEvent(event);
    assert.equal(control.value, 'system');
    assert.equal(tab.attributes.has('data-theme'), false);
  }
});

test('back navigation restores the latest choice instead of a cached theme', () => {
  const tab = page('dark');
  const { control } = tab.mount();
  tab.storage.set(key, 'light');
  tab.restore();
  assert.equal(control.value, 'light');
  assert.equal(tab.attributes.get('data-theme'), 'light');
  tab.storage.delete(key);
  tab.restore();
  assert.equal(control.value, 'system');
  assert.equal(tab.attributes.has('data-theme'), false);
});
