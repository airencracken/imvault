// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

export async function checkLibrary(page, base, photoID) {
  await page.goto(base + '/albums');
  const albumPath = await page.evaluate(`
    const form = new URLSearchParams({csrf_token: document.querySelector('meta[name="csrf-token"]').content, title: 'Library browser album', visibility: 'private', access: 'owner'});
    const response = await fetch('/albums', {method: 'POST', body: form});
    return new URL(response.url).pathname;
  `);
  assert.ok(albumPath.startsWith('/a/'));
  for (const enabled of [true, false]) {
    await page.send('Emulation.setScriptExecutionDisabled', {value: !enabled});
    try {
      await page.goto(base + '/gallery?sort=taken&year=unknown');
      await page.evaluate(`document.querySelector('input[name="files"][value="${photoID}"]').click();`);
      if (enabled) {
        assert.equal(await page.evaluate(`return document.querySelector('#bulk-count').textContent;`), '1 selected');
        assert.equal(await page.evaluate(`return document.querySelector('#bulk-album').disabled;`), false);
      }
      await page.evaluate(`
        const form = document.querySelector('#bulk-tags');
        form.elements.album.selectedIndex = [...form.elements.album.options].findIndex(o => o.textContent === 'Library browser album');
        document.querySelector('#bulk-album').click();
      `);
      await page.waitFor(`document.querySelector('.flash.ok')?.textContent.includes('Updated 1 selected file.')`);
      assert.equal(await page.evaluate(`return new URL(location.href).searchParams.get('year');`), 'unknown');
      await page.goto(base + albumPath);
      assert.ok(await page.evaluate(`return !!document.getElementById('file-${photoID}');`));
      await page.goto(base + '/gallery?sort=taken&year=unknown');
      await page.evaluate(`
        document.querySelector('input[name="files"][value="${photoID}"]').click();
        document.querySelector('select[name="visibility"]').value = 'private';
        document.querySelector('#bulk-visibility').click();
      `);
      await page.waitFor(`document.querySelector('.flash.ok')?.textContent.includes('Updated 1 selected file.')`);
      for (const width of [320, 390, 768, 1280]) {
        await page.send('Emulation.setDeviceMetricsOverride', {width, height: 900, deviceScaleFactor: 1, mobile: false});
        assert.equal(await page.evaluate(`return document.documentElement.scrollWidth <= innerWidth;`), true, 'gallery controls overflow at ' + width);
      }
      if (process.env.IMVAULT_SCREENSHOT_DIR) {
        await mkdir(process.env.IMVAULT_SCREENSHOT_DIR, {recursive:true});
        const shot = await page.send('Page.captureScreenshot', {captureBeyondViewport:true});
        await writeFile(join(process.env.IMVAULT_SCREENSHOT_DIR, 'library-gallery-' + (enabled ? 'js' : 'plain') + '.png'), Buffer.from(shot.data, 'base64'));
      }
      await page.goto(base + '/f/' + photoID);
      await page.evaluate(`document.querySelector('form[action$="/delete"] button').click();`);
      await page.waitFor(`document.querySelector('.flash.ok')?.textContent.includes('Moved to Trash')`);
      await page.goto(base + '/trash');
      await page.waitFor(`document.querySelector('img[src="/trash/${photoID}/thumb"]')?.naturalWidth > 0`);
      for (const width of [320, 390, 1280]) {
        await page.send('Emulation.setDeviceMetricsOverride', {width, height: 900, deviceScaleFactor: 1, mobile: false});
        assert.equal(await page.evaluate(`return document.documentElement.scrollWidth <= innerWidth;`), true, 'Trash overflows at ' + width);
      }
      if (process.env.IMVAULT_SCREENSHOT_DIR) {
        const shot = await page.send('Page.captureScreenshot', {captureBeyondViewport:true});
        await writeFile(join(process.env.IMVAULT_SCREENSHOT_DIR, 'library-trash-' + (enabled ? 'js' : 'plain') + '.png'), Buffer.from(shot.data, 'base64'));
      }
      await page.evaluate(`document.querySelector('form[action="/trash/${photoID}/restore"] button').click();`);
      await page.waitFor(`document.querySelector('.flash.ok')?.textContent.includes('Restored with its previous sharing settings')`);
      await page.goto(base + '/f/' + photoID);
      assert.ok(await page.evaluate(`return !!document.querySelector('.preview');`));
    } finally {
      await page.send('Emulation.setScriptExecutionDisabled', {value: false});
      await page.send('Emulation.clearDeviceMetricsOverride');
    }
  }
  // Explicit confirmation must remain functional with scripting disabled.
  const disposable = await page.evaluate(`
    const canvas = document.createElement('canvas'); canvas.width=3; canvas.height=3;
    const bytes=Uint8Array.from(atob(canvas.toDataURL('image/png').split(',')[1]),c=>c.charCodeAt(0));
    const form = new FormData(); form.append('csrf_token',document.querySelector('meta[name="csrf-token"]').content); form.append('files',new Blob([bytes],{type:'image/png'}),'permanent-delete.png');
    const response=await fetch('/upload',{method:'POST',body:form,headers:{'HX-Request':'true'}});
    return (await response.text()).match(/id="file-([a-z0-9]+)"/)?.[1];
  `);
  assert.ok(disposable);
  await page.send('Emulation.setScriptExecutionDisabled', {value:true});
  try {
    await page.goto(base+'/f/'+disposable);
    await page.evaluate(`document.querySelector('form[action$="/delete"] button').click();`);
    await page.waitFor(`document.querySelector('.flash.ok')?.textContent.includes('Moved to Trash')`);
    await page.goto(base+'/trash');
    await page.evaluate(`document.querySelector('form[action="/trash/${disposable}/purge"] input[name="confirm"]').click(); document.querySelector('form[action="/trash/${disposable}/purge"] button').click();`);
    await page.waitFor(`document.querySelector('.flash.ok')?.textContent.includes('Permanently deleted.')`);
    assert.equal(await page.evaluate(`return !!document.querySelector('form[action="/trash/${disposable}/purge"]');`),false);
  } finally { await page.send('Emulation.setScriptExecutionDisabled', {value:false}); }
  // Leave the existing fixture at its original sharing level for later checks.
  await page.goto(base+'/f/'+photoID);
  await page.evaluate(`
    const form=new URLSearchParams({csrf_token:document.querySelector('meta[name="csrf-token"]').content,visibility:'members'});
    await fetch('/f/${photoID}/visibility',{method:'POST',body:form});
  `);
}
