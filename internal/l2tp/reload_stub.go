//go:build !linux

package l2tp

import "fmt"

func Reload() error {
	return fmt.Errorf("xl2tpd reload is only supported on linux")
}
