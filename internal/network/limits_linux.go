//go:build linux

package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
)

func applyBandwidth(m model.TunnelMapping) error {
	return applyBandwidthContext(context.Background(), m)
}
func applyBandwidthContext(ctx context.Context, m model.TunnelMapping) error {
	if m.UploadKbps > 0 {
		if err := setRateQueue(ctx, m.TunName, m.UploadKbps); err != nil {
			return err
		}
	}
	if m.DownloadKbps > 0 {
		return applyDownloadQueue(ctx, m)
	}
	return nil
}

func setRateQueue(ctx context.Context, dev string, kbps int) error {
	return runContext(ctx, "tc", "qdisc", "replace", "dev", dev, "root", "handle", "1:", "tbf", "rate", fmt.Sprintf("%dkbit", kbps), "burst", fmt.Sprint(limitBurst(kbps)), "latency", "100ms")
}

func downloadDevice(m model.TunnelMapping) string {
	return "s2lifb" + strconv.Itoa(m.TableID)
}

type downloadLink struct {
	Kind  string
	Alias string
	Up    bool
}

func lookupDownloadLink(ctx context.Context, dev string) (*downloadLink, error) {
	if _, err := net.InterfaceByName(dev); err != nil {
		// An absent interface is normal at startup and after teardown.
		if e, ok := err.(*net.OpError); ok && e.Err.Error() == "no such network interface" {
			return nil, nil
		}
		return nil, err
	}
	out, err := outputContext(ctx, "ip", "-d", "-j", "link", "show", "dev", dev)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Alias string   `json:"ifalias"`
		Flags []string `json:"flags"`
		Link  struct {
			Kind string `json:"info_kind"`
		} `json:"linkinfo"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
		return nil, fmt.Errorf("invalid download interface snapshot for %s", dev)
	}
	link := &downloadLink{Alias: rows[0].Alias, Kind: rows[0].Link.Kind}
	for _, flag := range rows[0].Flags {
		link.Up = link.Up || flag == "UP"
	}
	return link, nil
}

func ownedDownloadLink(ctx context.Context, m model.TunnelMapping) (*downloadLink, error) {
	dev := downloadDevice(m)
	link, err := lookupDownloadLink(ctx, dev)
	if err == nil && link != nil && !downloadLinkOwned(link, m) {
		return nil, fmt.Errorf("download interface %s is already in use", dev)
	}
	return link, err
}

func downloadLinkOwned(link *downloadLink, m model.TunnelMapping) bool {
	return link != nil && link.Kind == "ifb" && link.Alias == "s2l-download:"+m.TunName
}

// An ingress policer drops bursts immediately and can stall gVisor TCP.
// Redirect to a bounded TBF queue before reinjecting through the original TUN.
func applyDownloadQueue(ctx context.Context, m model.TunnelMapping) (result error) {
	dev := downloadDevice(m)
	link, err := ownedDownloadLink(ctx, m)
	if err != nil {
		return err
	}
	if link == nil {
		if err := runContext(ctx, "ip", "link", "add", "name", dev, "type", "ifb"); err != nil {
			return err
		}
		defer func() {
			if result != nil {
				result = errors.Join(result, run("ip", "link", "del", "dev", dev))
			}
		}()
		if err := runContext(ctx, "ip", "link", "set", "dev", dev, "alias", "s2l-download:"+m.TunName); err != nil {
			return err
		}
	}
	if err := runContext(ctx, "ip", "link", "set", "dev", dev, "mtu", fmt.Sprint(m.EffectiveMTU()), "up"); err != nil {
		return err
	}
	if err := setRateQueue(ctx, dev, m.DownloadKbps); err != nil {
		return err
	}
	if err := runContext(ctx, "tc", "qdisc", "replace", "dev", m.TunName, "handle", "ffff:", "ingress"); err != nil {
		return err
	}
	// Old releases could leave several selectors at this reserved preference.
	filter, err := outputContext(ctx, "tc", "filter", "show", "dev", m.TunName, "parent", "ffff:", "pref", "10")
	if err != nil {
		return err
	}
	if count := strings.Count(filter, "match "); count > 1 || (count == 1 && !strings.Contains(filter, "fh 800::800 ")) {
		if err := runContext(ctx, "tc", "filter", "delete", "dev", m.TunName, "parent", "ffff:", "protocol", "ip", "pref", "10"); err != nil {
			return err
		}
	}
	// Replace the old policer at its existing preference and handle.
	return runContext(ctx, "tc", "filter", "replace", "dev", m.TunName, "parent", "ffff:", "protocol", "ip", "pref", "10", "handle", "800::800", "u32", "match", "u32", "0", "0", "action", "mirred", "egress", "redirect", "dev", dev)
}

func removeDownloadQueue(m model.TunnelMapping) error {
	if m.DownloadKbps == 0 {
		return nil
	}
	ctx := context.Background()
	dev := downloadDevice(m)
	link, err := lookupDownloadLink(ctx, dev)
	if err != nil {
		return err
	}
	if !downloadLinkOwned(link, m) {
		return nil
	}
	return runContext(ctx, "ip", "link", "del", "dev", dev)
}

func limitBurst(kbps int) int {
	v := kbps * 1000 / 8 / 100
	if v < 16384 {
		return 16384
	}
	return v
}

type rateQueue struct {
	Kind    string `json:"kind"`
	Handle  string `json:"handle"`
	Options struct {
		Rate uint64 `json:"rate"`
	} `json:"options"`
}

func rateQueues(ctx context.Context, dev string) ([]rateQueue, error) {
	out, err := outputContext(ctx, "tc", "-j", "qdisc", "show", "dev", dev)
	if err != nil {
		return nil, err
	}
	var queues []rateQueue
	if err := json.Unmarshal([]byte(out), &queues); err != nil {
		return nil, fmt.Errorf("invalid qdisc snapshot for %s: %w", dev, err)
	}
	return queues, nil
}

func hasRateQueue(queues []rateQueue, kbps int) bool {
	for _, q := range queues {
		if q.Kind == "tbf" && q.Handle == "1:" && q.Options.Rate == uint64(kbps)*125 {
			return true
		}
	}
	return false
}

func verifyBandwidth(ctx context.Context, m model.TunnelMapping) error {
	if m.UploadKbps == 0 && m.DownloadKbps == 0 {
		return nil
	}
	queues, err := rateQueues(ctx, m.TunName)
	if err != nil {
		return err
	}
	if m.UploadKbps > 0 && !hasRateQueue(queues, m.UploadKbps) {
		return missingState("upload limiter missing or rate changed", "bandwidth-upload")
	}
	if m.DownloadKbps == 0 {
		return nil
	}
	missing := missingState("download limiter missing or rate changed", "bandwidth-download")
	link, err := ownedDownloadLink(ctx, m)
	if err != nil {
		return err
	}
	if link == nil || !link.Up {
		return missing
	}
	dev := downloadDevice(m)
	download, err := rateQueues(ctx, dev)
	if err != nil {
		return err
	}
	if !hasRateQueue(download, m.DownloadKbps) {
		return missing
	}
	for _, q := range queues {
		if q.Kind == "ingress" && q.Handle == "ffff:" {
			filter, err := outputContext(ctx, "tc", "filter", "show", "dev", m.TunName, "parent", "ffff:", "pref", "10")
			if err != nil {
				return err
			}
			if validDownloadRedirect(filter, dev) {
				return nil
			}
		}
	}
	return missing
}

func validDownloadRedirect(filter, dev string) bool {
	return strings.Count(filter, "match ") == 1 &&
		strings.Contains(filter, "match 00000000/00000000 at 0") &&
		strings.Count(filter, "action order") == 1 &&
		strings.Contains(filter, "mirred (Egress Redirect to device "+dev+")")
}
