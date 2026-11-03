import test from 'node:test';
import assert from 'node:assert/strict';
import {managementAddresses} from '../src/utils/management-address.js';
test('management address suggestions require server-confirmed local addresses',()=>{
  assert.equal(managementAddresses({local_ipv4:['192.0.2.1']}).selected,'192.0.2.1');
  const info={local_ipv4:['192.0.2.1','10.0.0.1']};
  assert.equal(managementAddresses(info,'192.0.2.1').selected,'192.0.2.1');
  assert.equal(managementAddresses(info,'203.0.113.1').selected,'');
  assert.equal(managementAddresses(info,'panel.example').selected,'');
  assert.equal(managementAddresses({...info,access:{destination_ip:'192.0.2.8'}},'192.0.2.1').selected,'192.0.2.8');
  assert.deepEqual(managementAddresses({local_ipv4:['192.0.2.1','192.0.2.1','256.0.0.1','01.2.3.4','::1','<script>']}).addresses,['192.0.2.1']);
});
