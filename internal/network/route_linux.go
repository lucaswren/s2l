//go:build linux

package network

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
)

// AddPolicyRoute 添加策略路由：
//
//	ip rule add from <Subnet> table <TableID>
//	ip route add default dev <TunName> table <TableID>
func AddPolicyRoute(m model.TunnelMapping) error {
	if err := validateMapping(m); err != nil {
		return err
	}
	table := strconv.Itoa(m.TableID)

	// 先清理再添加，保证幂等
	if err := DelPolicyRoute(m); err != nil {
		return fmt.Errorf("clear old policy route: %w", err)
	}

	if err := run("ip", "rule", "add", "from", m.Subnet, "table", table); err != nil {
		return err
	}
	if err := run("ip", "route", "add", "default", "dev", m.TunName, "table", table); err != nil {
		// 回滚 rule，避免残留半套规则
		rollbackErr := run("ip", "rule", "del", "from", m.Subnet, "table", table)
		return errors.Join(err, rollbackErr)
	}
	return nil
}

// DelPolicyRoute 删除策略路由（忽略不存在的规则）
func DelPolicyRoute(m model.TunnelMapping) error {
	if m.Subnet == "" || m.TableID == 0 {
		return nil
	}
	table := strconv.Itoa(m.TableID)
	var errs []error
	if err := run("ip", "route", "flush", "table", table); err != nil {
		// A fresh mapping has no kernel FIB table yet. This is already clean.
		if !strings.Contains(strings.ToLower(err.Error()), "fib table does not exist") {
			errs = append(errs, err)
		}
	}
	// 同一 from+table 可能残留多条，循环清理
	for i := 0; i < 8; i++ {
		err := run("ip", "rule", "del", "from", m.Subnet, "table", table)
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
