// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';

export async function checkProfiles(page, base) {
 for (const enabled of [true,false]) {
  await page.send('Emulation.setScriptExecutionDisabled',{value:!enabled});
  try {
   await page.goto(base+'/settings/account');
   await page.evaluate(`document.querySelector('a[href="/settings/profile"]').click();`);
   await page.waitFor(`!!document.querySelector('input[name="profile_name"]')`);
   await page.evaluate(`
    const form=document.querySelector('form[action="/settings/profile"]');
    form.elements.profile_name.value='A demo name';
    form.elements.profile_bio.value='A short bio. Books and rainy walks.';
    form.querySelector('input[name="profile_link_label"]').value='My site';
    form.querySelector('input[name="profile_link_url"]').value='https://example.org/';
    form.querySelector('button[type="submit"]').click();
   `);
   await page.waitFor(`document.querySelector('.flash.ok')?.textContent.includes('Profile saved.')`);
   assert.equal(await page.evaluate(`return document.querySelector('input[name="profile_name"]').value;`),'A demo name');
   const profile=await page.evaluate(`return [...document.querySelectorAll('a')].find(a=>a.textContent==='View your profile').getAttribute('href');`);
   await page.goto(base+profile);
   assert.equal(await page.evaluate(`return document.querySelector('h1').textContent;`),'boss');
   assert.equal(await page.evaluate(`return [...document.querySelectorAll('a')].find(a=>a.textContent==='My site').target;`),'_blank');
   assert.ok(await page.evaluate(`return document.querySelector('main').textContent.includes('Books and rainy walks.');`));
   for (const width of [320,390,1280]) {
    await page.send('Emulation.setDeviceMetricsOverride',{width,height:900,deviceScaleFactor:1,mobile:false});
    assert.equal(await page.evaluate(`return document.documentElement.scrollWidth<=innerWidth;`),true);
   }
   await page.goto(base+'/settings/profile');
   await page.evaluate(`
    const form=document.querySelector('form[action="/settings/profile"]');
    form.elements.profile_name.value='';form.elements.profile_bio.value='';
    form.querySelectorAll('input[name="profile_link_label"],input[name="profile_link_url"]').forEach(input=>input.value='');
    form.querySelector('button[type="submit"]').click();
   `);
   await page.waitFor(`document.querySelector('.flash.ok')?.textContent.includes('Profile saved.')`);
   await page.goto(base+profile);
   assert.ok(await page.evaluate(`return document.querySelector('main').textContent.includes('hasn’t added a bio');`));
  } finally {
   await page.send('Emulation.setScriptExecutionDisabled',{value:false});
   await page.send('Emulation.clearDeviceMetricsOverride');
  }
 }
}
