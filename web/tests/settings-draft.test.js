import test from 'node:test';
import assert from 'node:assert/strict';
import {mergeSettingsDraft} from '../src/utils/settings-draft.js';

test('refresh updates untouched settings while retaining unsaved edits', () => {
 const old={username:'admin',port:22,enabled:false};
 const current={username:'draft-name',port:22,enabled:false,password:'unsaved-secret'};
 const next={username:'server-name',port:2222,enabled:true};
 assert.deepEqual(mergeSettingsDraft(current,old,next),{username:'draft-name',port:2222,enabled:true,password:'unsaved-secret'});
 assert.equal(current.port,22);
});
test('initial load and explicit post-save reset accept server normalization', () => {
 assert.deepEqual(mergeSettingsDraft({timezone:'local',password:''},null,{timezone:'Asia/Shanghai'}),{timezone:'Asia/Shanghai',password:''});
});
test('time refresh retains typed NTP servers but updates untouched timezone', () => {
 const old={timezone:'UTC',servers:'old.example',auto_sync:true};
 const current={...old,servers:'new.example'};
 assert.deepEqual(mergeSettingsDraft(current,old,{timezone:'Asia/Shanghai',servers:'server.example',auto_sync:false}),{timezone:'Asia/Shanghai',servers:'new.example',auto_sync:false});
});
