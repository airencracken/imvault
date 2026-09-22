import assert from 'node:assert/strict';

export async function checkThemes(page, base) {
  const media = value => page.send('Emulation.setEmulatedMedia', {
    features: [{ name: 'prefers-color-scheme', value }],
  });
  const background = () => page.evaluate('return getComputedStyle(document.body).backgroundColor;');
  const choose = value => page.evaluate(`
    const control = document.querySelector('[data-theme-select]');
    control.value = ${JSON.stringify(value)};
    control.dispatchEvent(new Event('change', { bubbles: true }));
  `);
  await media('dark');
  await page.goto(base + '/login');
  assert.equal(await page.evaluate('return document.querySelector("[data-theme-select]").value;'), 'system');
  assert.equal(await background(), 'rgb(16, 18, 22)');
  await media('light');
  assert.equal(await background(), 'rgb(243, 245, 249)');
  await choose('dark');
  await page.goto(base + '/login');
  assert.equal(await background(), 'rgb(16, 18, 22)');
  assert.equal(await page.evaluate('return document.querySelector("[data-theme-select]").value;'), 'dark');
  await choose('light');
  await media('dark');
  assert.equal(await background(), 'rgb(243, 245, 249)');
  await page.goto(base + '/tags');
  assert.equal(await page.evaluate('return document.querySelector("[data-theme-select]").value;'), 'light');
  assert.equal(await background(), 'rgb(243, 245, 249)');
  await choose('system');
  assert.equal(await page.evaluate('return localStorage.getItem("imvault-theme");'), null);
  assert.equal(await background(), 'rgb(16, 18, 22)');

  // CSS still follows the device when scripts are disabled; hide the unusable switch.
  await page.send('Emulation.setScriptExecutionDisabled', { value: true });
  try {
    await page.goto(base + '/login');
    assert.equal(await background(), 'rgb(16, 18, 22)');
    assert.equal(await page.evaluate('return getComputedStyle(document.querySelector(".theme-picker")).display;'), 'none');
  } finally {
    await page.send('Emulation.setScriptExecutionDisabled', { value: false });
    await media('light');
  }
}
