//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

// AddPolicyRoute 添加策略路由：
//
//	ip rule add from <Subnet> table <TableID>
//	ip route add default dev <TunName> table <TableID>
func AddPolicyRoute(m model.TunnelMapping) (result error) {
	if err := validateMapping(m); err != nil {
		return err
	}
	table := strconv.Itoa(m.TableID)

	// Refuse foreign state before changing this table.
	if err := checkRouteOwnership(context.Background(), m); err != nil {
		return err
	}
	// 先清理本映射规则，保留其他配置
	if err := DelPolicyRoute(m); err != nil {
		return fmt.Errorf("clear old policy route: %w", err)
	}
	defer func() {
		if result != nil {
			result = errors.Join(result, DelPolicyRoute(m))
		}
	}()

	if err := run("ip", "route", "add", "unreachable", "default", "metric", "32767", "table", table); err != nil {
		return err
	}
	if err := run("ip", "rule", "add", "priority", strconv.Itoa(10000+m.TableID), "from", m.Subnet, "table", table, "priority", strconv.Itoa(10000+m.TableID)); err != nil {
		return err
	}
	if err := run("ip", "route", "add", "default", "dev", m.TunName, "metric", "100", "table", table); err != nil {
		return err
	}
	return nil
}

// DelPolicyRoute 删除策略路由（忽略不存在的规则）
func DelPolicyRoute(m model.TunnelMapping) error {
	if m.Subnet == "" || m.TableID == 0 {
		return nil
	}
	// Persisted mappings also pass here during cleanup; never flush system tables.
	if err := validateMapping(m); err != nil {
		return err
	}
	table := strconv.Itoa(m.TableID)
	var errs []error
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rules, routes, err := routeSnapshot(ctx, m)
	if err != nil {
		return err
	}
	if err = validateRouteOwnership(m, rules, routes); err != nil {
		return err
	}
	for _, route := range routes {
		args := []string{"route", "del", "unreachable", "default", "metric", "32767", "table", table}
		if route.Dev == m.TunName {
			args = []string{"route", "del", "default", "dev", m.TunName, "metric", "100", "table", table}
		}
		if err := runContext(ctx, "ip", args...); err != nil && !strings.Contains(strings.ToLower(err.Error()), "no such") {
			errs = append(errs, err)
		}
	}
	if len(rules) == 0 {
		return errors.Join(errs...)
	}
	// 同一 from+table 可能残留多条，循环清理
	for i := 0; i < 8; i++ {
		err := run("ip", "rule", "del", "from", m.Subnet, "table", table, "priority", strconv.Itoa(10000+m.TableID))
		if err == nil {
			continue
		}
		if strings.Contains(strings.ToLower(err.Error()), "no such file or directory") {
			break
		}
		errs = append(errs, err)
		break
	}
	return errors.Join(errs...)
}
