// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';

export async function checkAvatars(page, base, directory) {
 const single=Buffer.from('R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7','base64');
 const animated=Buffer.concat([single.subarray(0,19),single.subarray(19,-1),single.subarray(19,-1),Buffer.from([0x3b])]);
 const path=join(directory,'avatar.gif');await writeFile(path,animated);
 for (const enabled of [true,false]) {
  await page.send('Emulation.setScriptExecutionDisabled',{value:!enabled});
  try {
   await page.goto(base+'/settings/profile');
   const {root}=await page.send('DOM.getDocument');
   const {nodeId}=await page.send('DOM.querySelector',{nodeId:root.nodeId,selector:'input[name="avatar"]'});
   await page.send('DOM.setFileInputFiles',{nodeId,files:[path]});
   await page.evaluate(`document.querySelector('form[action="/settings/avatar"] button').click();`);
   await page.waitFor(`document.querySelector('.flash.ok')?.textContent.includes('Avatar saved.') && document.querySelector('.member-avatar img')?.naturalWidth>0`);
   const avatar=await page.evaluate(`return document.querySelector('.member-avatar img').getAttribute('src');`);
   const profile=await page.evaluate(`return [...document.querySelectorAll('a')].find(a=>a.textContent==='View your profile').getAttribute('href');`);
   await page.goto(base+profile);
   await page.waitFor(`document.querySelector('.member-avatar img')?.naturalWidth>0`);
   assert.equal(await page.evaluate(`return document.querySelector('.member-avatar img').getAttribute('src');`),avatar);
   await page.send('Emulation.setEmulatedMedia',{features:[{name:'prefers-reduced-motion',value:'reduce'}]});
   await page.waitFor(`document.querySelector('.member-avatar img')?.currentSrc.endsWith('?still=1')`);
   assert.equal(await page.evaluate(`return document.documentElement.scrollWidth<=innerWidth;`),true);
   await page.send('Emulation.setEmulatedMedia',{features:[{name:'prefers-reduced-motion',value:'no-preference'}]});
   await page.goto(base+'/settings/profile');
   await page.evaluate(`
    const form=document.querySelector('form[action="/settings/avatar-preference"]');
    form.elements.animate.checked=false;form.querySelector('button').click();
   `);
   await page.waitFor(`document.querySelector('.flash.ok')?.textContent.includes('Animation preference saved.')`);
   assert.equal(await page.evaluate(`return (await fetch(${JSON.stringify(avatar)})).headers.get('Content-Type');`),'image/png');
   for (const width of [320,390,1280]) {
    await page.send('Emulation.setDeviceMetricsOverride',{width,height:900,deviceScaleFactor:1,mobile:false});
    assert.equal(await page.evaluate(`return document.documentElement.scrollWidth<=innerWidth;`),true);
   }
   await page.evaluate(`document.querySelector('button[name="remove"]').click();`);
   await page.waitFor(`document.querySelector('.flash.ok')?.textContent.includes('Avatar saved.') && !document.querySelector('.member-avatar')`);
   assert.equal(await page.evaluate(`return document.querySelector('input[name="animate"]').checked;`),false);
   await page.evaluate(`
    const form=document.querySelector('form[action="/settings/avatar-preference"]');
    form.elements.animate.checked=true;form.querySelector('button').click();
   `);
   await page.waitFor(`document.querySelector('.flash.ok')?.textContent.includes('Animation preference saved.')`);
  } finally {
   await page.send('Emulation.setScriptExecutionDisabled',{value:false});
   await page.send('Emulation.clearDeviceMetricsOverride');
   await page.send('Emulation.setEmulatedMedia',{features:[]});
  }
 }
}
