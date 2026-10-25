import test from 'node:test';
import assert from 'node:assert/strict';
import {clientAccessText} from '../src/utils/client-access.js';
import {tr, setLanguage} from '../src/i18n/index.js';

test('connection guidance uses server DNS and never copies passwords or PSK', () => {
  const mapping={tun_name:'tun101',l2tp_user:'gateway',l2tp_pass:'private-dial-password',dns_tcp_address:'private.resolver:5353'};
  const settings={client_dns:['1.1.1.1','8.8.8.8'],ipsec:{enabled:true,psk:'private-psk'}};
  for (const language of ['en','zh-TW','zh-CN']) {
    setLanguage(language);
    const text=clientAccessText('vpn.example',mapping,settings,tr);
    for(const expected of ['vpn.example','tun101','gateway','L2TP/IPsec','1.1.1.1, 8.8.8.8','IPv4','IPv6']) assert.ok(text.includes(expected),text);
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
