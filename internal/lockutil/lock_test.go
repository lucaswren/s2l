package lockutil

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestCancellationDoesNotWaitForOwnerOrStealLock(t *testing.T) {
	var mu sync.Mutex
	mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Lock(ctx, &mu) }()
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled lock wait hung")
	}
	mu.Unlock()
	if err := Lock(context.Background(), &mu); err != nil {
		t.Fatal(err)
	}
	mu.Unlock()
	if err := Lock(ctx, &mu); err != context.Canceled {
		t.Fatal("cancelled request acquired free lock", err)
	}
}

func TestReadLockCancellationDoesNotDelayWriter(t *testing.T) {
	var mu sync.RWMutex
	mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RLock(ctx, &mu) }()
	cancel()
	select {
	case e := <-done:
		if e != context.Canceled {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("read lock ignored cancellation")
	}
	mu.Unlock()
	if e := RLock(context.Background(), &mu); e != nil {
		t.Fatal(e)
	}
	mu.RUnlock()
}
