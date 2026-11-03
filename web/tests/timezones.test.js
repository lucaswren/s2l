import test from 'node:test';
import assert from 'node:assert/strict';
import { timezoneGroups } from '../src/api/timezones.js';

const groups = (zones, current, date = '2026-01-01') => timezoneGroups(zones, current, date);

test('offset groups are numerically sorted and contain unique valid zones', () => {
  const result = groups(['Asia/Shanghai', 'Asia/Shanghai', 'America/New_York', 'Asia/Taipei', 'Asia/Kathmandu', 'invalid/zone', '+08:00', 'Etc/UTC'], 'Asia/Tokyo');
  assert.deepEqual(result.map(g => g.label), ['UTC-05:00', 'UTC+00:00', 'UTC+05:45', 'UTC+08:00', 'UTC+09:00']);
  assert.deepEqual(result.flatMap(g => g.options.map(o => o.value)), ['America/New_York', 'UTC', 'Asia/Kathmandu', 'Asia/Shanghai', 'Asia/Taipei', 'Asia/Tokyo']);
});

test('keeps the selected server alias and shows the offset at the reference instant', () => {
  assert.deepEqual(groups(['UTC', 'Etc/UTC'], 'Etc/UTC')[0].options.map(o => o.value), ['Etc/UTC']);
  assert.equal(groups(['America/New_York'], '', '2026-07-01')[0].label, 'UTC-04:00');
});
