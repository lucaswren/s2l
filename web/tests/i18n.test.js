import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import {fileURLToPath} from 'node:url';
import { tr, setLanguage, normalizeLanguage } from '../src/i18n/index.js';
import { healthLabels } from '../src/utils/health.js';
import {mappingGroups,mappingGroupKey} from '../src/utils/mapping-groups.js';
const messages=JSON.parse(readFileSync(new URL('../src/i18n/messages.json',import.meta.url),'utf8'));
test('all catalog translations retain placeholders and have no environment branding',()=>{
  for(const[key,entry]of Object.entries(messages)) {
    for(const lang of ['en','tw']) {
      assert.ok(entry[lang]?.trim(),`${lang}: ${key}`);
      assert.deepEqual((entry[lang].match(/\{\d+\}/g)||[]).sort(),(key.match(/\{\d+\}/g)||[]).sort(),key);
      assert.doesNotMatch(entry[lang],/panabit|ikuai|软路由|軟路由/i);
    }
  }
});
test('all literal translation calls have catalog entries',()=>{
  function scan(dir){for(const entry of readdirSync(dir,{withFileTypes:true})){const name=join(dir,entry.name);if(entry.isDirectory()){if(entry.name!=='i18n')scan(name);continue}if(!/\.(vue|js)$/.test(name))continue;const source=readFileSync(name,'utf8');for(const match of source.matchAll(/\btr\(("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')/g)){const key=match[1][0]==='"'?JSON.parse(match[1]):match[1].slice(1,-1).replaceAll("\\'","'");assert.ok(Object.hasOwn(messages,key),`${name}: ${key}`)}}}
  scan(fileURLToPath(new URL('../src',import.meta.url)));
});
test('language selection, live labels, fallback and interpolation',()=>{
  assert.equal(normalizeLanguage('zh-Hant-HK'),'zh-TW');assert.equal(normalizeLanguage('en-GB'),'en');assert.equal(normalizeLanguage('de'),'zh-CN');
  setLanguage('en');assert.equal(tr('保存'),'Save');assert.equal(healthLabels.healthy,'Healthy');
  assert.equal(tr('增加 {0}','<script>{1}</script>'),'Add <script>{1}</script>');
  assert.equal(tr('unrecognized external error'),'unrecognized external error');
  setLanguage('zh-TW');assert.equal(tr('保存'),'保存');assert.equal(tr('服务器'),'伺服器');
  setLanguage('zh-CN');assert.equal(tr('保存'),'保存');
});
test('group filter values survive language changes and cannot collide with all-groups',()=>{
  const rows=[{group:''},{group:'*'},{group:'未分组'}];
  setLanguage('zh-CN');const before=mappingGroups(rows);const key=mappingGroupKey(rows[0]);
  setLanguage('en');const after=mappingGroups(rows);
  assert.deepEqual(before.map(row=>row.value),after.map(row=>row.value));
  assert.equal(after.find(row=>row.value===key).label,'Ungrouped');
  assert.notEqual(mappingGroupKey(rows[1]),'*');assert.notEqual(mappingGroupKey(rows[2]),key);
  setLanguage('zh-CN');
});
