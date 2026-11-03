//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/lucaswren/s2l/internal/l2tp"
	"github.com/lucaswren/s2l/internal/lockutil"
	"github.com/lucaswren/s2l/internal/model"
)

// The guard keys on the kernel interface index, which changes even when pppd
// reuses a name. It runs before the existing filter/NAT mapping rules.
const sessionTable = "s2l_ppp_sessions"

func (c *Controller) EnableSessionGuard() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ensureSessionFirewall(); err != nil {
		return err
	}
	if err := replaceSessionGuard(context.Background(), nil); err != nil {
		return err
	}
	c.sessionGuard = true
	c.admitted = nil
	return nil
}

// Reconcile from actual point-to-point PPP interfaces, independently of the
// advisory datagram hooks. Keep the first live owner until it disappears.
func (c *Controller) SyncSessionGuard(ctx context.Context) error {
	if err := lockutil.Lock(ctx, &c.mu); err != nil {
		return err
	}
	defer c.mu.Unlock()
	return c.syncSessionGuard(ctx)
}

func (c *Controller) VerifyAdmittedPeerRoutes(ctx context.Context, m model.TunnelMapping, peers []model.PPPSession) error {
	if err := lockutil.Lock(ctx, &c.mu); err != nil {
		return err
	}
	enabled, owner := c.sessionGuard, c.admitted[m.ID]
	c.mu.Unlock()
	if !enabled {
		return VerifyPeerRoutes(ctx, m, peers)
	}
	for _, peer := range peers {
		if peer.MappingID == m.ID && peer.Index == owner.Index && peer.PeerIP == owner.PeerIP {
			return VerifyPeerRoutes(ctx, m, []model.PPPSession{peer})
		}
	}
	for _, peer := range peers {
		if peer.MappingID == m.ID {
			return fmt.Errorf("PPP session forwarding has not been admitted")
		}
	}
	return ctx.Err()
}

func (c *Controller) syncSessionGuard(ctx context.Context) error {
	if !c.sessionGuard {
		return nil
	}
	mappings := make([]model.TunnelMapping, 0, len(c.active))
	for _, mapping := range c.active {
		mappings = append(mappings, mapping)
	}
	rows, err := readPeerInterfaces(ctx, mappings)
	if err != nil {
		// Keep existing index-bound entries on inspection failure. Newly
		// created interfaces are still blocked by the kernel guard.
		return err
	}
	next := selectSessionPeers(mappings, rows, c.admitted)
	var pinErrors error
	for _, m := range mappings {
		row, ok := next[m.ID]
		if ok && row.Index != c.admitted[m.ID].Index {
			if err := pinSessionReturn(ctx, m, row); err != nil {
				delete(next, m.ID)
				pinErrors = errors.Join(pinErrors, fmt.Errorf("pin return route for %s: %w", m.ID, err))
			}
		}
	}
	if err := replaceSessionGuard(ctx, next); err != nil {
		return err
	}
	c.admitted = next
	return pinErrors
}

func selectSessionPeers(mappings []model.TunnelMapping, rows []model.PPPSession, previous map[string]model.PPPSession) map[string]model.PPPSession {
	expected := make(map[string]string, len(mappings))
	for _, mapping := range mappings {
		if peer, err := l2tp.ClientIPFromSubnet(mapping.Subnet); err == nil {
			expected[mapping.ID] = peer
		}
	}
	next := make(map[string]model.PPPSession, len(mappings))
	for _, row := range rows {
		if row.Index <= 0 || !strings.HasPrefix(row.Interface, "ppp") || row.PeerIP == "" || row.PeerIP != expected[row.MappingID] {
			continue
		}
		old, found := next[row.MappingID]
		owner := previous[row.MappingID]
		if !found || row.Index == owner.Index || (old.Index != owner.Index && row.Index < old.Index) {
			next[row.MappingID] = row
		}
	}
	return next
}

func replaceSessionGuard(parent context.Context, peers map[string]model.PPPSession) error {
	var rules strings.Builder
	fmt.Fprintf(&rules, "add table ip %[1]s\ndelete table ip %[1]s\nadd table ip %[1]s\n", sessionTable)
	fmt.Fprintf(&rules, "add set ip %s peers { type ipv4_addr . iface_index; }\n", sessionTable)
	for _, row := range peers {
		fmt.Fprintf(&rules, "add element ip %s peers { %s . %d }\n", sessionTable, row.PeerIP, row.Index)
	}
	for _, hook := range []string{"input", "forward"} {
		fmt.Fprintf(&rules, "add chain ip %s %s { type filter hook %s priority -10; policy accept; }\n", sessionTable, hook, hook)
		// pppst* is reserved for the root-owned disposable forwarding probe.
		fmt.Fprintf(&rules, "add rule ip %s %s iifname \"ppp*\" iifname != \"pppst*\" ip saddr . meta iif != @peers drop\n", sessionTable, hook)
	}
	fmt.Fprintf(&rules, "add rule ip %s forward oifname \"ppp*\" oifname != \"pppst*\" ip daddr . meta oif != @peers drop\n", sessionTable)
	// Only packets that passed both applicable peer checks reach this point.
	// The disposable root-owned proof interfaces use the same handshake.
	for _, direction := range []string{"iifname", "oifname"} {
		fmt.Fprintf(&rules, "add rule ip %s forward %s \"ppp*\" meta mark set meta mark | %s\n", sessionTable, direction, sessionMark)
	}
	return applySessionGuard(parent, rules.String())
}

func applySessionGuard(ctx context.Context, rules string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// nft expands "-" to /dev/stdin, which may be absent in a private /dev.
	cmd := exec.CommandContext(ctx, "nft", "-f", "/proc/self/fd/0")
	cmd.Stdin = strings.NewReader(rules)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("PPP session guard: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func removeSessionGuard() error {
	// The transaction also succeeds if the table has already been removed.
	return applySessionGuard(context.Background(), "add table ip "+sessionTable+"\ndelete table ip "+sessionTable+"\n")
}
