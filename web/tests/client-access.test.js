import test from 'node:test';
import assert from 'node:assert/strict';
import {clientAccessMode, clientAccessText} from '../src/utils/client-access.js';
import {tr, setLanguage} from '../src/i18n/index.js';

test('connection guidance uses server DNS and never copies passwords or PSK', () => {
  const mapping={tun_name:'tun101',l2tp_user:'gateway',l2tp_pass:'private-dial-password',dns_tcp_address:'private.resolver:5353'};
  const settings={client_dns:['8.8.8.8','1.1.1.1'],ipsec:{available:true,enabled:true,active:true,protected:true,psk:'private-psk'}};
  for (const language of ['en','zh-TW','zh-CN']) {
    setLanguage(language);
    const text=clientAccessText('vpn.example',mapping,settings,tr);
    for(const expected of ['vpn.example','tun101','gateway','L2TP/IPsec','8.8.8.8, 1.1.1.1','IPv4','IPv6']) assert.ok(text.includes(expected),text);
    assert.doesNotMatch(text,/private-dial-password|private-psk|private\.resolver/);
  }
});

test('unavailable server settings are not presented as confirmed connection parameters', () => {
  setLanguage('en');
  const text=clientAccessText('vpn.example',{},null,tr);
  assert.match(text,/Waiting for server DNS parameters/);
  assert.doesNotMatch(text,/1\.1\.1\.1|8\.8\.8\.8|L2TP\/IPsec/);
  setLanguage('zh-CN');
});

test('access mode requires complete runtime state and detects inconsistent protection', () => {
  const plain = {available: true, enabled: false, active: false, protected: false};
  assert.equal(clientAccessMode(plain), 'plain');
  assert.equal(clientAccessMode({...plain, available: false}), 'unknown');
  assert.equal(clientAccessMode({available: true, enabled: true}), 'unknown');
  assert.equal(clientAccessMode({...plain, enabled: true, active: true}), 'error');
  assert.equal(clientAccessMode({...plain, enabled: true, protected: true}), 'error');
  assert.equal(clientAccessMode({...plain, protected: true}), 'error');
  assert.equal(clientAccessMode({...plain, enabled: true, active: true, protected: true}), 'ipsec');
});
