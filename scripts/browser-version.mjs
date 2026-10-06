// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';

export async function checkVersionDisplay(page, base, expectedVersion) {
  for (const enabled of [true, false]) {
    await page.send('Emulation.setScriptExecutionDisabled', { value: !enabled });
    try {
      await page.goto(`${base}/admin/settings`);
      const product = await page.evaluate(`return [...document.querySelectorAll('p')].find(p => p.textContent.startsWith('Running Imvault ')).textContent.trim().replace(/^Running /, '').replace(/\.$/, '');`);
      assert.equal(product, expectedVersion);
      assert.equal(await page.evaluate(`return document.querySelector('input[name="show_version"]').checked;`), false);
      for (const visible of [true, false]) {
        await page.evaluate(`
          const checkbox = document.querySelector('input[name="show_version"]');
          checkbox.checked = ${visible};
          checkbox.form.querySelector('button[type="submit"]').click();
        `);
        await page.waitFor(`document.querySelector('input[name="show_version"]')?.checked === ${visible} && document.querySelector('.flash.ok')`);
        assert.equal(await page.evaluate(`return document.querySelector('footer').textContent.includes(${JSON.stringify(product)});`), visible);
        await page.goto(`${base}/about`);
        assert.equal(await page.evaluate(`return document.querySelector('footer').textContent.includes(${JSON.stringify(product)});`), visible);
        await page.goto(`${base}/admin/settings`);
        assert.equal(await page.evaluate(`return document.querySelector('input[name="show_version"]').checked;`), visible);
      }
    } finally {
      await page.send('Emulation.setScriptExecutionDisabled', { value: false });
    }
  }
}
