// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

export async function checkAccountPages(page, base) {
  for (const path of ['/login', '/register']) {
    await page.goto(base + path);
    for (const width of [320, 1280]) {
      await page.send('Emulation.setDeviceMetricsOverride', { width, height: 960, deviceScaleFactor: 1, mobile: false });
      assert.ok(await page.evaluate(`
        const form = document.querySelector('.account-page').getBoundingClientRect();
        return document.documentElement.scrollWidth <= innerWidth && form.width <= 480 && form.left >= 0 && form.right <= innerWidth;
      `), `Imvault compact ${path} form at ${width}px`);
    }
  }
  await page.send('Emulation.clearDeviceMetricsOverride');
}

export async function checkInvitations(page, base) {
  await page.goto(base + '/admin/invites');
  for (const disabled of [false, true]) {
    await page.send('Emulation.setScriptExecutionDisabled', { value: disabled });
    await page.goto(base + '/admin/invites');
    for (const theme of ['light', 'dark']) {
      await page.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: theme }] });
      if (!disabled) await page.evaluate(`document.querySelector('[data-theme-select]').value = 'system'; document.querySelector('[data-theme-select]').dispatchEvent(new Event('change'));`);
      for (const width of [320, 390, 768, 1280]) {
        await page.send('Emulation.setDeviceMetricsOverride', { width, height: 960, deviceScaleFactor: 1, mobile: false });
        assert.ok(await page.evaluate(`
          const form = document.querySelector('.invitation-create').getBoundingClientRect();
          const list = document.querySelector('.invitation-list').getBoundingClientRect();
          return document.documentElement.scrollWidth <= innerWidth &&
            (innerWidth > 900 ? form.right < list.left && Math.abs(form.top - list.top) < 1 : form.bottom < list.top) &&
            [...document.querySelectorAll('.invitation-create input:not([type="hidden"])')].every(input => {
              const bounds = input.getBoundingClientRect();
              return bounds.width >= 90 && bounds.height >= 44 && bounds.left >= 0 && bounds.right <= innerWidth;
            }) && !document.querySelector('.eyebrow, .invitation-page table');
        `), `Imvault invitation layout at ${width}px (${theme}, scripts disabled: ${disabled})`);
        if (process.env.IMVAULT_SCREENSHOT_DIR && !disabled && [390, 1280].includes(width)) {
          await mkdir(process.env.IMVAULT_SCREENSHOT_DIR, { recursive: true });
          const { data } = await page.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
          await writeFile(join(process.env.IMVAULT_SCREENSHOT_DIR, `invitations-${theme}-${width}.png`), Buffer.from(data, 'base64'));
        }
      }
    }
    await page.send('Emulation.setScriptExecutionDisabled', { value: false });
  }
  await page.send('Emulation.clearDeviceMetricsOverride');
  await page.send('Emulation.setEmulatedMedia', { features: [] });
}
