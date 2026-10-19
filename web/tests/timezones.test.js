import test from 'node:test';
import assert from 'node:assert/strict';
import { timezoneGroups } from '../src/api/timezones.js';
const options = (...args) => timezoneGroups(...args).flatMap(g => g.options);
test('timezone options are unique, grouped and keep the current server zone', () => {
 const groups = timezoneGroups(['Asia/Shanghai','UTC','Asia/Shanghai'], 'Asia/Tokyo', '2026-01-01');
 assert.equal(groups[0].label, '协调世界时');
 assert.deepEqual(groups.flatMap(g => g.options).map(o => o.value), ['UTC','Asia/Shanghai','Asia/Tokyo']);
 assert.match(groups[1].options[0].label, /UTC\+08:00/);
 assert.match(groups[1].options[0].label, /中国/);
});
test('offsets use the reference instant including DST and fractional offsets', () => {
 assert.match(options(['America/New_York'], '', '2026-01-01')[0].label, /UTC-05:00/);
 assert.match(options(['America/New_York'], '', '2026-07-01')[0].label, /UTC-04:00/);
 assert.match(options(['Asia/Kathmandu'], '', '2026-01-01')[0].label, /UTC\+05:45/);
});
test('browser-unknown server zones remain selectable without inventing offsets', () => {
 assert.equal(options(['Future/City'], '', '2026-01-01')[0].label, 'Future/City');
});
