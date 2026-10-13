package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/service"
	"github.com/lucaswren/s2l/internal/store"
)

func TestConcurrentMappingCreatesRejectConflicts(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNode(model.SocksNode{ID: "n", Addr: "127.0.0.1:1080"}); err != nil {
		t.Fatal(err)
	}
	svc := service.NewMappingService(st, nil, nil, nil)
	s := NewServer(st, svc, nil)
	handler := s.Handler()
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make(chan int, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			body := fmt.Sprintf(`{"node_id":"n","tun_name":"tun101","table_id":101,"subnet":"10.0.101.0/24","l2tp_user":"u%d","l2tp_pass":"password"}`, i)
			r := httptest.NewRequest(http.MethodPost, "/api/mappings", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			codes <- w.Code
		}(i)
	}
	close(start)
	wg.Wait()
	close(codes)
	success := 0
	for code := range codes {
		if code == http.StatusOK {
			success++
		} else if code != http.StatusBadRequest {
			t.Fatalf("unexpected status %d", code)
		}
	}
	if success != 1 || len(st.ListMappings()) != 1 {
		t.Fatalf("conflicting creates succeeded: %d", success)
	}
}

func TestNodeIDMustBeAddressable(t *testing.T) {
	for _, id := range []string{"bad/id", "bad?id", "bad#id", "bad id", ".", ".."} {
		if err := validateSocksNode(model.SocksNode{ID: id, Addr: "127.0.0.1:1080"}); err == nil {
			t.Fatalf("accepted inaccessible node ID %q", id)
		}
	}
}
