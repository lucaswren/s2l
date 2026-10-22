package singbox

import (
	"context"
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
	"os"
	"os/exec"
	"time"
)

// CheckRuntime validates the exact generated schema without starting TUN or DNS.
func CheckRuntime(binary string) error {
	data, err := GenerateConfig(model.SocksNode{Addr: "127.0.0.1:1080"}, model.TunnelMapping{TunName: "s2lcheck", TableID: 101, Subnet: "10.0.101.0/24"}, "127.0.0.1:1053")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "s2l-config-check-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "check", "-c", f.Name())
	cmd.WaitDelay = time.Second
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sing-box compatibility check: %w: %s", err, output)
	}
	return nil
}
