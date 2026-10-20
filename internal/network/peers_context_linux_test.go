//go:build linux

package network

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPeerCollectionRespectsCancellation(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "ip"), []byte("#!/bin/sh\nexec sleep 20\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := ReadPeersContext(ctx, nil)
	if err == nil || time.Since(started) > time.Second {
		t.Fatal("peer collection ignored cancellation", err, time.Since(started))
	}
}
