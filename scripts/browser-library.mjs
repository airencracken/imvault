// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

async function checkBulkAlbumCreation(page, base, photoID, enabled) {
  const title = enabled ? 'Bulk browser album with scripts' : 'Bulk browser album without scripts';
  if (enabled) {
    assert.equal(await page.evaluate(`return document.querySelector('#bulk-album-create').getBoundingClientRect().height;`), 0, 'New album fields must be visually hidden before choosing creation');
  }
  await page.evaluate(`
    document.querySelector('input[name="files"][value="${photoID}"]').click();
    const select = document.querySelector('#bulk-album-select');
    select.value = 'new';
    select.dispatchEvent(new Event('change', {bubbles:true}));
  `);
  assert.equal(await page.evaluate(`return document.querySelector('#bulk-album-create').hidden;`), false);
  assert.ok(await page.evaluate(`return document.querySelector('#bulk-album-create').getBoundingClientRect().height > 0;`), 'New album fields must be visible when creating');
  if (enabled) {
    await page.evaluate(`document.querySelector('#bulk-album').click();`);
    assert.equal(await page.evaluate(`return document.querySelector('#bulk-album-error').hidden;`), false);
    assert.equal(await page.evaluate(`return document.querySelector('input[name="files"][value="${photoID}"]').checked;`), true, 'A missing title keeps the selected photo');
    assert.equal(await page.evaluate(`return document.activeElement.id;`), 'bulk-album-title');
  }
  await page.evaluate(`
    const input = document.querySelector('#bulk-album-title');
    input.value = ${JSON.stringify(title)};
    input.dispatchEvent(new Event('input', {bubbles:true}));
  `);
  for (const width of [320, 390, 768, 1280]) {
    await page.send('Emulation.setDeviceMetricsOverride', {width, height: 900, deviceScaleFactor: 1, mobile: false});
    assert.equal(await page.evaluate(`return document.documentElement.scrollWidth <= innerWidth;`), true, 'New album fields overflow at ' + width);
  }
  await page.evaluate(`document.querySelector('#bulk-album').click();`);
  await page.waitFor(`document.querySelector('.flash.ok')?.textContent.includes('Updated 1 selected file.')`);
  assert.equal(await page.evaluate(`return new URL(location.href).searchParams.get('year');`), 'unknown');
  assert.equal(await page.evaluate(`return [...document.querySelectorAll('#bulk-album-select option')].filter(o => o.textContent === ${JSON.stringify(title)}).length;`), 1, 'New album appears in the picker');
  await page.goto(base + '/albums');
  const path = await page.evaluate(`return [...document.querySelectorAll('.album-card')].find(a => a.querySelector('strong')?.textContent === ${JSON.stringify(title)})?.getAttribute('href');`);
  assert.ok(path?.startsWith('/a/'), 'New album can be opened');
  await page.goto(base + path);
  assert.equal(await page.evaluate(`return !!document.getElementById('file-${photoID}');`), true, 'Selected photo is attached to the new album');
  await page.goto(base + '/gallery?sort=taken&year=unknown');
  if (enabled) {
    await page.evaluate(`
      const select = document.querySelector('#bulk-album-select');
      select.value = 'new'; select.dispatchEvent(new Event('change', {bubbles:true}));
      select.selectedIndex = [...select.options].findIndex(o => o.textContent === ${JSON.stringify(title)});
      select.dispatchEvent(new Event('change', {bubbles:true}));
    `);
    assert.equal(await page.evaluate(`return document.querySelector('#bulk-album-create').getBoundingClientRect().height;`), 0, 'Switching to an existing album hides the creation fields');
  }
}

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
      await checkBulkAlbumCreation(page, base, photoID, enabled);
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
