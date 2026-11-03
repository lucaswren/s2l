//go:build linux

package evidence

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func Available() bool { _, err := exec.LookPath("tcpdump"); return err == nil }

// CollectSystem never invokes a shell or changes the host network. Missing
// optional utilities are recorded per command, without disabling snapshots.
func CollectSystem(ctx context.Context, req Request) Collected {
	result := Collected{Snapshots: map[string]string{}, Capture: "unavailable"}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); result.PCAP, result.Capture = capturePackets(ctx, req) }()
	commands := []struct {
		name string
		args []string
	}{
		{"routes", []string{"ip", "-4", "route", "show", "table", "all"}},
		{"rules", []string{"ip", "-4", "rule", "show"}},
		{"interfaces", []string{"ip", "-s", "addr", "show"}},
		{"tcp", []string{"ss", "-tin"}},
		{"firewall", []string{"iptables-save", "-c"}},
		{"l2tp-nat", []string{"conntrack", "-L", "-p", "udp", "--dport", "1701"}},
		{"queues", []string{"tc", "-s", "qdisc", "show"}},
		{"journal", []string{"journalctl", "-u", "s2l", "-u", "xl2tpd", "--since", "-3 min", "-n", "150", "--no-pager", "-o", "short-iso"}},
	}
	for _, command := range commands {
		result.Snapshots[command.name] = commandText(ctx, command.args)
	}
	for _, path := range []string{"/proc/net/snmp", "/proc/net/netstat", "/proc/net/dev", "/proc/loadavg", "/proc/meminfo", "/proc/sys/net/netfilter/nf_conntrack_count", "/proc/sys/net/netfilter/nf_conntrack_max"} {
		f, err := os.Open(path)
		if err != nil {
			result.Snapshots[path] = err.Error()
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(f, 32<<10))
		f.Close()
		result.Snapshots[path] = string(b)
	}
	wg.Wait()
	// A second snapshot makes counter deltas and post-detection changes visible.
	result.Snapshots["routes-after"] = commandText(ctx, []string{"ip", "-4", "route", "show", "table", "all"})
	result.Snapshots["tcp-after"] = commandText(ctx, []string{"ss", "-tin"})
	return result
}

type cappedText struct {
	data      []byte
	truncated bool
}

func (w *cappedText) Write(b []byte) (int, error) {
	n := len(b)
	left := (32 << 10) - len(w.data)
	if len(b) > left {
		b = b[:left]
		w.truncated = true
	}
	w.data = append(w.data, b...)
	return n, nil
}
func commandText(parent context.Context, args []string) string {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	w := &cappedText{}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	configureCommand(cmd)
	cmd.Stdout = w
	cmd.Stderr = w
	err := cmd.Run()
	text := string(w.data)
	if err != nil {
		text += "\n[command: " + err.Error() + "]"
	}
	if w.truncated {
		text += "\n[truncated at 32 KiB]"
	}
	return text
}
func capturePackets(parent context.Context, req Request) ([]byte, string) {
	if !Available() {
		return nil, "unavailable: tcpdump missing"
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	terms := []string{"udp port 1701", "icmp"}
	if _, subnet, err := net.ParseCIDR(req.Mapping.Subnet); err == nil {
		terms = append(terms, "net "+subnet.String())
	}
	if req.Mapping.TableID > 0 {
		terms = append(terms, fmt.Sprintf("host 172.19.%d.1", req.Mapping.TableID%256))
	}
	if term := nodeCaptureTerm(req.Node.Addr); term != "" {
		terms = append(terms, term)
	}
	filter := "ip and (" + strings.Join(terms, " or ") + ")"
	cmd := exec.CommandContext(ctx, "tcpdump", "-i", "any", "-p", "-nn", "-U", "-s", "160", "-c", "4096", "-w", "-", filter)
	configureCommand(cmd)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "unavailable: capture pipe"
	}
	// Never keep raw stdout or application data, even on decoder failure.
	stderr := &cappedText{}
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		return nil, "unavailable: cannot start tcpdump"
	}
	pcap, count, decodeErr := HeaderPCAP(pipe)
	cancel()
	_ = pipe.Close()
	waitErr := cmd.Wait()
	if decodeErr != nil && len(pcap) == 0 {
		return nil, "unavailable: invalid or empty capture"
	}
	if count == 0 {
		return nil, "unavailable: no matching packets"
	}
	state := fmt.Sprintf("headers: %d packets (maximum 20s / 4096 packets)", count)
	if decodeErr != nil {
		state += "; partial stream"
	} else if waitErr != nil && parent.Err() != nil {
		state += "; cancelled"
	}
	return pcap, state
}

func nodeCaptureTerm(addr string) string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil || host == "" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			return "(tcp and host " + ip.To4().String() + ")"
		}
		return "" // Capture is explicitly IPv4-only.
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return ""
	}
	// Do not delay outage capture on the very DNS service under investigation.
	// Domain nodes match their TCP port; this may include other nodes on that
	// port, but the same header-only filter and packet/time limits still apply.
	return fmt.Sprintf("(tcp port %d)", n)
}

func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
}
