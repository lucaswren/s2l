package singbox

import (
	"os"
	"testing"
)

func TestRuntimeCompatibilityCheck(t *testing.T) {
	if binary := os.Getenv("S2L_SINGBOX_CHECK_BIN"); binary != "" {
		if err := CheckRuntime(binary); err != nil {
			t.Fatal(err)
		}
	}
	if err := CheckRuntime("s2l-missing-binary"); err == nil {
		t.Fatal("missing binary accepted")
	}
}
