package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
)

func TestParseNodeTextFormats(t *testing.T) {
	text := `
# comment
host:port:user:pass
1.2.3.4:1080:u1:p1
u2:p2@5.6.7.8:1080
9.9.9.9:1080@u3:p3
socks5://u4:p4@10.0.0.1:1080
线路A,1.2.3.4:1081,u5,p5
线路B|5.6.7.8:443|u6|p6
11.11.11.11,1080,u7,p7
8.8.8.8:1080
`
	nodes, errs := parseNodeText(text)
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	if len(nodes) != 8 {
		t.Fatalf("got %d nodes: %+v", len(nodes), nodes)
	}
	assertNode := func(i int, addr, user, pass string) {
		t.Helper()
		if nodes[i].Addr != addr || nodes[i].Username != user || nodes[i].Password != pass {
			t.Fatalf("node[%d]=%+v want addr=%s user=%s pass=%s", i, nodes[i], addr, user, pass)
		}
	}
	assertNode(0, "1.2.3.4:1080", "u1", "p1")
	assertNode(1, "5.6.7.8:1080", "u2", "p2")
	assertNode(2, "9.9.9.9:1080", "u3", "p3")
	assertNode(3, "10.0.0.1:1080", "u4", "p4")
	if nodes[4].Name != "线路A" {
		t.Fatalf("named: %+v", nodes[4])
	}
	assertNode(4, "1.2.3.4:1081", "u5", "p5")
	assertNode(5, "5.6.7.8:443", "u6", "p6")
	assertNode(6, "11.11.11.11:1080", "u7", "p7")
	assertNode(7, "8.8.8.8:1080", "", "")
}

func TestParseNodeColonPasswordWithColon(t *testing.T) {
	n, err := parseNodeLine("1.2.3.4:1080:user:p:ass")
	if err != nil {
		t.Fatal(err)
	}
	if n.Password != "p:ass" {
		t.Fatalf("pass=%q", n.Password)
	}
}

func TestSplitFields(t *testing.T) {
	parts := splitFields("101,线路A,user101,pass101")
	if len(parts) != 4 {
		t.Fatalf("parts=%v", parts)
	}
}

func TestNodeImportSpecialCredentials(t *testing.T) {
	for _, tc := range []struct{ line, addr, user, pass string }{
		{"Name,proxy.example:1080,user@example.com,secret", "proxy.example:1080", "user@example.com", "secret"},
		{"proxy.example:1080:user@example.com:p@ss", "proxy.example:1080", "user@example.com", "p@ss"},
		{"proxy.example:1080@user:p@ss", "proxy.example:1080", "user", "p@ss"},
		{"user:p@ss@proxy.example:1080", "proxy.example:1080", "user", "p@ss"},
		{"socks5://user:1234@[2001:db8::1]:1080", "[2001:db8::1]:1080", "user", "1234"},
		{"socks5://%20user%20:%20p%40ss%20@proxy.example:1080", "proxy.example:1080", " user ", " p@ss "},
	} {
		nodes, errs := parseNodeText(tc.line)
		if len(errs) != 0 || len(nodes) != 1 {
			t.Fatalf("valid import rejected: %v", errs)
		}
		n := nodes[0]
		if n.Addr != tc.addr || n.Username != tc.user || n.Password != tc.pass {
			t.Fatal("import changed connection identity")
		}
	}
	for _, line := range []string{"user:1234@proxy.example:1080", "proxy.example:1080:user@example.com:1234", "name,proxy.example:1080,user,pass,extra", "proxy.example:1080,user,pass,extra", "socks5://user:pass@proxy.example:1080/path", "socks5://user:pass@proxy.example:1080?ignored"} {
		if _, err := parseNodeLine(line); err == nil {
			t.Fatal("ambiguous or silently truncated input accepted")
		}
	}
}

func TestSharedProxyImportPreservesAccountsAndMetadata(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	node := model.SocksNode{ID: "n", Name: "saved", Group: "group", Addr: "proxy.example:1080", Username: "a@provider", Password: " first "}
	if err := st.UpsertNode(node); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMapping(model.TunnelMapping{ID: "m", NodeID: "n", Status: model.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	h := NewServer(st, nil, nil).Handler()
	post := func(body importNodesReq) importResult {
		t.Helper()
		encoded, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/api/nodes/import", bytes.NewReader(encoded))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var result importResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal(w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "first") || strings.Contains(w.Body.String(), "second-secret") {
			t.Fatal("import response exposed a password")
		}
		return result
	}
	text := "socks5://a%40provider:%20first%20@proxy.example:1080\nsocks5://b:second-secret@proxy.example:1080\nsocks5://a%40provider:second-secret@proxy.example:1080"
	result := post(importNodesReq{Text: text})
	if result.Created != 2 || result.Updated != 1 || result.Skipped != 0 || len(st.ListNodes()) != 3 {
		t.Fatal(result)
	}
	if got, _ := st.GetNode("n"); got != node {
		t.Fatal("reimport changed active node credentials or metadata")
	}
	result = post(importNodesReq{Text: text})
	if result.Created != 0 || result.Updated != 3 || result.Skipped != 0 {
		t.Fatal("reimport is not idempotent", result)
	}
	changed := node
	changed.Password = "replacement"
	result = post(importNodesReq{Nodes: []model.SocksNode{changed}})
	if result.Skipped != 1 || result.Updated != 0 || len(result.Errors) != 1 {
		t.Fatal("explicit import changed an active node", result)
	}
	if got, _ := st.GetNode("n"); got != node {
		t.Fatal("active node was overwritten")
	}
	result = post(importNodesReq{Text: "invalid-line\nsocks5://user:second-secret@proxy.example:1080/ignored"})
	if result.Skipped != 2 || len(result.Errors) != 2 || result.Created != 0 || result.Updated != 0 {
		t.Fatal("invalid import lost row diagnostics", result)
	}
	s := NewServer(st, nil, nil)
	if _, errs := parseMappingText("101,proxy.example:1080,user,password", s); len(errs) != 1 || !strings.Contains(errs[0], "ambiguous") {
		t.Fatal("ambiguous proxy address picked an arbitrary exit", errs)
	}
	if maps, errs := parseMappingText("101,n,user,password", s); len(errs) != 0 || len(maps) != 1 || maps[0].NodeID != "n" {
		t.Fatal("explicit node ID did not resolve", errs)
	}
}
