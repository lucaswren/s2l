// Package lockutil bounds diagnostic waits without changing lifecycle ownership.
package lockutil

import (
	"context"
	"sync"
	"time"
)

func Lock(ctx context.Context, mu *sync.Mutex) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mu.TryLock() {
		return nil
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if err := ctx.Err(); err != nil {
				return err
			}
			if mu.TryLock() {
				return nil
			}
		}
	}
}

// RLock lets canceled diagnostics leave a pending configuration writer alone.
func RLock(ctx context.Context, mu *sync.RWMutex) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mu.TryRLock() {
		return nil
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if err := ctx.Err(); err != nil {
				return err
			}
			if mu.TryRLock() {
				return nil
			}
		}
	}
}
