//go:build !linux && !darwin

package api

import "context"

// The Python settings helper is deployed on Linux. Other platforms use settingsMu.
func lockSettingsFile(_ context.Context, _ string) (func(), error) {
	return func() {}, nil
}
