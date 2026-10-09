//go:build !linux

package network

func TuneInterface(name string, mtu int) error { return nil }
