package api

import (
	"testing"
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
