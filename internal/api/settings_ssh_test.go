package api

import (
	"slices"
	"strings"
	"testing"
)

func TestActiveSSHSocketPorts(t *testing.T) {
	output := "Listen=0.0.0.0:2222 (Stream) [::]:2222 (Stream)\n" +
		"Listen=127.0.0.1:22 (Stream)\nListen=/run/ssh.sock (Stream)\nActiveState=active\n"
	if ports := activeSSHSocketPorts(output); !slices.Equal(ports, []int{22, 2222}) {
		t.Fatalf("unexpected active SSH ports: %v", ports)
	}
	inactive := strings.Replace(output, "ActiveState=active", "ActiveState=inactive", 1)
	if ports := activeSSHSocketPorts(inactive); len(ports) != 0 {
		t.Fatalf("inactive socket reported live ports: %v", ports)
	}
}
