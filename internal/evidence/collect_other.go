//go:build !linux

package evidence

import "context"

func Available() bool { return false }
func CollectSystem(ctx context.Context, req Request) Collected {
	return Collected{Snapshots: map[string]string{"platform": "Kernel network evidence requires Linux; status history is retained."}, Capture: "unavailable: platform"}
}
