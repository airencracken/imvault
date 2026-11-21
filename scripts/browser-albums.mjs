// SPDX-License-Identifier: AGPL-3.0-or-later

import assert from 'node:assert/strict';

export async function checkAlbumLayout(page, base, fileID) {
  const path = '/a/browser-shared';
  const discussion = 'https://board.example/topics/' + 'long-path/'.repeat(100);
  const description = '<picnic> & ' + 'unbroken'.repeat(40);

  await page.evaluate(`
    const form = document.querySelector('.album-settings form');
    form.elements.description.value = ${JSON.stringify(description)};
    const settings = await fetch(form.getAttribute('action'), {method: 'POST', body: new URLSearchParams(new FormData(form))});
    if (!settings.ok) throw new Error('settings update failed');
    const link = document.querySelector('.album-discussions form[action$="/discussion"]');
    link.elements.url.value = ${JSON.stringify(discussion)};
    const linked = await fetch(link.getAttribute('action'), {method: 'POST', body: new URLSearchParams(new FormData(link))});
    if (!linked.ok) throw new Error('linking a discussion failed');
  `);
  await page.goto(base + path);
  await page.waitFor(`document.querySelector('.album-media #file-${fileID} img')?.naturalWidth > 0`);
  assert.ok(await page.evaluate(`
    const media = document.querySelector('.album-media');
    return media.querySelector('#file-${fileID} .thumb img') &&
      document.querySelector('.album-description').textContent === ${JSON.stringify(description)} &&
      !document.querySelector('.album-description picnic') &&
      !document.querySelector('.album-picker').open &&
      !document.querySelector('.album-settings').open &&
      !document.querySelector('.album-discussions').open &&
      !!(media.compareDocumentPosition(document.querySelector('.album-options')) & Node.DOCUMENT_POSITION_FOLLOWING);
  `), 'Photos should open above the discussion and settings disclosures');

  await page.send('Emulation.setTouchEmulationEnabled', {enabled: true});
  try {
    for (const width of [320, 390, 768, 1280]) {
      await page.send('Emulation.setDeviceMetricsOverride', {width, height: 900, deviceScaleFactor: 1, mobile: false});
      assert.ok(await page.evaluate(`return document.documentElement.scrollWidth <= innerWidth;`), 'Album overflows at ' + width);
      const columns = await page.evaluate(`return getComputedStyle(document.querySelector('.album-media .grid')).gridTemplateColumns.split(' ').length;`);
      assert.ok(columns >= 2, 'Album should show at least two thumbnail columns at ' + width);
    }
    await page.send('Emulation.setDeviceMetricsOverride', {width: 320, height: 900, deviceScaleFactor: 1, mobile: false});
    const removal = await page.evaluate(`
      const button = document.querySelector('.album-media .card-action button');
      return {size: button.getBoundingClientRect().width, opacity: getComputedStyle(button.form).opacity};
    `);
    assert.ok(removal.size >= 44 && removal.opacity === '1', 'Remove needs a visible touch target');

    await page.evaluate(`
      for (const details of document.querySelectorAll('.album-page details')) details.open = true;
    `);
    assert.ok(await page.evaluate(`return document.documentElement.scrollWidth <= innerWidth;`), 'Opened controls and a long discussion link should fit a narrow display');
    assert.equal(await page.evaluate(`return document.querySelector('.album-discussion-links a').href;`), discussion);
    await page.evaluate(`document.querySelector('.album-delete button').click();`);
    await page.waitFor(`document.querySelector('#confirm-dialog')?.open`);
    assert.equal(await page.evaluate(`return document.getElementById('confirm-message').textContent;`), 'Delete this album?');
    await page.evaluate(`document.querySelector('#confirm-dialog [data-confirm-cancel]').click();`);
    await page.waitFor(`!document.querySelector('#confirm-dialog').open && location.pathname === '${path}'`);
  } finally {
    await page.send('Emulation.setTouchEmulationEnabled', {enabled: false});
    await page.send('Emulation.clearDeviceMetricsOverride');
  }
}

export async function checkSharedAlbumCard(page, base) {
  const card = '.album-card:has(a[href="/a/browser-shared"])';
  try {
    for (const width of [320, 390, 768, 1280]) {
      await page.send('Emulation.setDeviceMetricsOverride', {width, height: 900, deviceScaleFactor: 1, mobile: false});
      const layout = await page.evaluate(`
        const card = document.querySelector(${JSON.stringify(card)});
        const title = card?.querySelector('.album-card-title');
        const owner = card?.querySelector('.album-byline a');
        const badges = card ? [...card.querySelectorAll('.badge')] : [];
        if (!title || !owner || !badges.length) return {};

        const box = card.getBoundingClientRect();
        const ownerBox = owner.getBoundingClientRect();
        const inside = [title, owner, ...badges].every(part => {
          const bounds = part.getBoundingClientRect();
          return bounds.width > 0 && bounds.height > 0 &&
            bounds.left >= box.left && bounds.right <= box.right &&
            bounds.top >= box.top && bounds.bottom <= box.bottom;
        });
        return {
          inside,
          ownerTarget: document.elementFromPoint(ownerBox.x + ownerBox.width / 2, ownerBox.y + ownerBox.height / 2) === owner,
          albumTarget: document.elementFromPoint(box.right - 8, box.top + 8) === title,
        };
      `);
      assert.equal(layout.inside, true, 'The shared album border should enclose the title, member name and badges at ' + width + 'px');
      assert.equal(layout.ownerTarget, true, 'The member name should open its profile at ' + width + 'px');
      assert.equal(layout.albumTarget, true, 'The empty space inside the card should open the album at ' + width + 'px');
    }
  } finally {
    await page.send('Emulation.clearDeviceMetricsOverride');
  }

  const profile = await page.evaluate(`
    const card = document.querySelector(${JSON.stringify(card)});
    const owner = card?.querySelector('.album-byline a');
    if (!owner || card.querySelectorAll('a').length !== 2 || document.querySelector('.new-album').open) return '';
    setTimeout(() => owner.click(), 0);
    return owner.pathname;
  `);
  assert.match(profile, /^\/members\/\d+$/);
  await page.waitFor(`location.pathname === ${JSON.stringify(profile)}`);
  await page.goto(base + '/albums');
  await page.evaluate(`setTimeout(() => document.querySelector('.album-card-title[href="/a/browser-shared"]').click(), 0);`);
  await page.waitFor(`location.pathname === '/a/browser-shared' && document.querySelector('.album-media .card')`);
  assert.ok(await page.evaluate(`
    return !document.querySelector('.album-settings, .album-delete') &&
      !!document.querySelector('.album-picker');
  `), 'A member can contribute to a shared album and only the owner can edit it');
}
