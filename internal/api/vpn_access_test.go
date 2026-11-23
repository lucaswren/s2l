package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestVPNAddressIsIndependentOfManagementURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := `{"public_url":"https://panel.example:8443","unknown":{"preserve":true}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewServer(nil, nil, nil, "admin", "fixture-password")
	s.ConfigureSettings(path)
	h := s.Handler()
	const endpoint = "/api/settings/vpn-address"
	if got := authRequest(h, "POST", endpoint, `{"address":"vpn.example"}`, nil); got.Code != 401 {
		t.Fatal(got.Code)
	}
	login := authRequest(h, "POST", "/api/auth/login", `{"username":"admin","password":"fixture-password"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	for _, body := range []string{`{}`, `{"address":null}`, `{"address":"https://vpn.example"}`, `{"address":"192.0.2.5:1701"}`, `{"address":"256.0.0.1"}`, `{"address":"::1"}`} {
		if got := authRequest(h, "POST", endpoint, body, cookie); got.Code != 400 {
			t.Errorf("%s returned %d", body, got.Code)
		}
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != original {
		t.Fatalf("invalid request modified config: %v", err)
	}
	for _, address := range []string{"vpn.example", "192.0.2.5", ""} {
		body, _ := json.Marshal(map[string]string{"address": address})
		got := authRequest(h, "POST", endpoint, string(body), cookie)
		if got.Code != 200 {
			t.Fatal(got.Code, got.Body.String())
		}
		raw, _ := os.ReadFile(path)
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			t.Fatal("invalid saved JSON")
		}
		var saved string
		if json.Unmarshal(fields["vpn_address"], &saved) != nil || saved != address ||
			string(fields["public_url"]) != `"https://panel.example:8443"` || fields["unknown"] == nil {
			t.Fatalf("incorrect persisted settings for %q", address)
		}
		refresh := authRequest(h, "GET", "/api/settings", "", cookie)
		var status map[string]any
		if refresh.Code != 200 || json.Unmarshal(refresh.Body.Bytes(), &status) != nil || status["vpn_address"] != address {
			t.Fatalf("settings refresh did not return %q", address)
		}
	}
	if got := authRequest(h, "GET", "/api/dial-timeline", "", nil); got.Code != 401 {
		t.Fatal(got.Code)
	}
}

func TestDialTimelineEmitsOnlyClassifiedEvidence(t *testing.T) {
	for _, tc := range []struct{ source, message, stage string }{
		{"xl2tpd", "Connection established to 192.0.2.5", "l2tp-control"},
		{"xl2tpd", "Call established with 192.0.2.5", "l2tp-call"},
		{"pppd", "Peer private-user failed CHAP authentication", "ppp-auth-failed"},
		{"pppd", "CHAP authentication succeeded", "ppp-auth-ok"},
		{"pppd", "remote IP address 10.0.0.2", "ppp-address"},
		{"pppd", "Connection terminated.", "ppp-closed"},
		{"charon", "received packet: from 192.0.2.1[500] to 192.0.2.2[500] (328 bytes)", "ike-udp"},
		{"charon", "IKE_SA s2l-l2tp[1] established between test endpoints", "ike-sa"},
		{"charon", "CHILD_SA s2l-l2tp{1} established", "ipsec-sa"},
		{"charon", "no shared key found for private identity", "ike-failed"},
		{"charon", "02[ENC] could not decrypt payloads", "ike-failed"},
		{"charon", "02[IKE] message parsing failed", ""},
		{"unrelated", "CHAP authentication succeeded", ""},
		{"pppd", "pppd started", ""},
	} {
		line, _ := json.Marshal(map[string]string{"SYSLOG_IDENTIFIER": tc.source, "MESSAGE": tc.message, "__REALTIME_TIMESTAMP": "1800000000000000"})
		event, ok := dialEventFromJournal(string(line))
		if event.Stage != tc.stage || ok != (tc.stage != "") || (ok && event.At.UnixMicro() != 1800000000000000) {
			t.Errorf("%q -> %+v (%v)", tc.message, event, ok)
		}
	}
	if _, ok := dialEventFromJournal(`{"SYSLOG_IDENTIFIER":"pppd","MESSAGE":"CHAP authentication succeeded"}`); ok {
		t.Fatal("timestamp-free record was accepted")
	}
}
