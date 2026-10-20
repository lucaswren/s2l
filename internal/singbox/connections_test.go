package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDisplayedConnectionCanCloseAfterListOrderChanges(t *testing.T) {
	deleted := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleted = r.URL.Path
			w.WriteHeader(204)
			return
		}
		rows := []map[string]string{}
		for i := 0; i < 501; i++ {
			rows = append(rows, map[string]string{"id": fmt.Sprint(i)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"connections": rows})
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	rows, e := readConnections(context.Background(), address, "")
	if e != nil || !rows.Truncated || len(rows.Rows) != 500 {
		t.Fatal(rows, e)
	}
	if e = closeConnection(context.Background(), address, "", "500"); e != nil || deleted != "/connections/500" {
		t.Fatal(deleted, e)
	}
}
