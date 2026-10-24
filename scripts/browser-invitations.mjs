// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { execFileSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';

const moduleDirectory = execFileSync('go', ['list', '-m', '-f', '{{.Dir}}', 'github.com/airencracken/comfylib'], { encoding: 'utf8' }).trim();
const { accountLayout, invitationLayout, assertInvitationLayout, accountWidths, invitationWidths } = await import(pathToFileURL(join(moduleDirectory, 'ui/uitest/layout.mjs')));

export async function checkAccountPages(page, base) {
  for (const path of ['/login', '/register']) {
    await page.goto(base + path);
    for (const width of accountWidths) {
      await page.send('Emulation.setDeviceMetricsOverride', { width, height: 960, deviceScaleFactor: 1, mobile: false });
      assert.ok(await page.evaluate(`return (${accountLayout.toString()})();`), `Imvault compact ${path} form at ${width}px`);
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
      for (const width of invitationWidths) {
        await page.send('Emulation.setDeviceMetricsOverride', { width, height: 960, deviceScaleFactor: 1, mobile: false });
        const layout = await page.evaluate(`return (${invitationLayout.toString()})();`);
        assertInvitationLayout(layout, width, `Imvault (${theme}, scripts disabled: ${disabled})`);
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
