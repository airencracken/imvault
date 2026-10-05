// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

export async function checkPhotoRotation(page, base) {
  // A rectangular fixture makes sideways geometry observable in the browser.
  const uploaded = await page.evaluate(`
    const canvas = document.createElement('canvas');
    canvas.width = 30; canvas.height = 20;
    const context = canvas.getContext('2d');
    context.fillStyle = '#fc8120'; context.fillRect(0, 0, 30, 20);
    context.fillStyle = '#176070'; context.fillRect(0, 0, 10, 10);
    const source = canvas.toDataURL('image/png').split(',')[1];
    const bytes = Uint8Array.from(atob(source), c => c.charCodeAt(0));
    const form = new FormData();
    form.append('csrf_token', document.querySelector('meta[name="csrf-token"]').content);
    form.append('visibility', 'private');
    form.append('files', new Blob([bytes], { type: 'image/png' }), 'sideways.png');
    const response = await fetch('/upload', { method: 'POST', body: form, headers: { 'HX-Request': 'true' } });
    const html = await response.text();
    return { id: html.match(/id="file-([a-z0-9]+)"/)?.[1], source };
  `);
  assert.ok(uploaded.id);
  const photo = `${base}/f/${uploaded.id}`;
  for (const enabled of [true, false]) {
    await page.send('Emulation.setScriptExecutionDisabled', { value: !enabled });
    try {
      await page.goto(photo);
      const original = await page.evaluate(`return document.querySelector('.preview img').getAttribute('src');`);
      await page.evaluate(`document.querySelector('button[name="direction"][value="right"]').click();`);
      await page.waitFor(`document.querySelector('button[name="direction"][value="reset"]') && document.querySelector('.preview img')?.complete && document.querySelector('.preview img')?.naturalWidth === 20`);
      const rotated = await page.evaluate(`
        const image = document.querySelector('.preview img');
        return { width: image.naturalWidth, height: image.naturalHeight, src: image.getAttribute('src'), download: document.querySelector('a[href$="/rotated"]')?.textContent };
      `);
      assert.equal(rotated.width, 20); assert.equal(rotated.height, 30);
      assert.notEqual(rotated.src, original);
      assert.equal(rotated.download, 'Download rotated photo');
      await page.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: false });
      assert.equal(await page.evaluate(`return document.documentElement.scrollWidth <= innerWidth;`), true, 'rotation controls overflow on mobile');
      if (process.env.IMVAULT_SCREENSHOT_DIR) {
        await mkdir(process.env.IMVAULT_SCREENSHOT_DIR, { recursive: true });
        const shot = await page.send('Page.captureScreenshot', { captureBeyondViewport: true });
        await writeFile(join(process.env.IMVAULT_SCREENSHOT_DIR, `photo-rotation-${enabled ? 'js' : 'plain'}.png`), Buffer.from(shot.data, 'base64'));
      }
      await page.evaluate(`document.querySelector('button[name="direction"][value="left"]').click();`);
      await page.waitFor(`!document.querySelector('button[name="direction"][value="reset"]') && document.querySelector('.preview img')?.complete && document.querySelector('.preview img')?.naturalWidth === 30`);
      assert.equal(await page.evaluate(`return document.querySelector('.preview img').getAttribute('src');`), original);
      await page.evaluate(`document.querySelector('button[name="direction"][value="left"]').click();`);
      await page.waitFor(`document.querySelector('button[name="direction"][value="reset"]') && document.querySelector('.preview img')?.naturalWidth === 20`);
      await page.evaluate(`document.querySelector('button[name="direction"][value="reset"]').click();`);
      await page.waitFor(`!document.querySelector('button[name="direction"][value="reset"]') && document.querySelector('.preview img')?.naturalWidth === 30`);
    } finally {
      await page.send('Emulation.setScriptExecutionDisabled', { value: false });
      await page.send('Emulation.clearDeviceMetricsOverride');
    }
  }
  await page.goto(photo);
  const originalBytes = await page.evaluate(`
    const response = await fetch('/f/${uploaded.id}/raw');
    const data = new Uint8Array(await response.arrayBuffer());
    return btoa(String.fromCharCode(...data));
  `);
  assert.equal(originalBytes, uploaded.source, 'turns changed the original');
}
