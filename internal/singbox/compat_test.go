package singbox

import (
	"github.com/lucaswren/s2l/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Optional validation with the exact executable installed by the project.
func TestSingBoxRuntimeConfigCheck(t *testing.T) {
	bin := os.Getenv("S2L_SINGBOX_CHECK_BIN")
	if bin == "" {
		t.Skip("set S2L_SINGBOX_CHECK_BIN to validate with sing-box")
	}
	for _, policy := range []string{"reject-quic", "reject", "allow"} {
		t.Run(policy, func(t *testing.T) {
			m := model.TunnelMapping{TunName: "tun101", TableID: 101, UDPPolicy: policy}
			data, err := GenerateConfig(model.SocksNode{Addr: "1.2.3.4:1080"}, m, "127.0.0.1:19053")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(bin, "check", "-c", path).CombinedOutput(); err != nil {
				t.Fatalf("sing-box rejected generated configuration: %v\n%s", err, out)
			}
		})
	}
}
