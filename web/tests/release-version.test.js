import assert from 'node:assert/strict';
import test from 'node:test';
import {isOlderStableRelease} from '../src/utils/release-version.js';

test('software update orders stable tags with or without the v prefix', () => {
  assert.equal(isOlderStableRelease('1.0.5', '1.0.6'), true);
  assert.equal(isOlderStableRelease('v1.9.9', '1.10.0'), true);
  assert.equal(isOlderStableRelease('1.0.10', 'v1.0.9'), false);
  assert.equal(isOlderStableRelease('1.0.5', 'v1.0.5'), false);
  assert.equal(isOlderStableRelease('1.0.5', 'development'), false);
  assert.equal(isOlderStableRelease('1.0.5-rc1', '1.0.6'), false);
});
