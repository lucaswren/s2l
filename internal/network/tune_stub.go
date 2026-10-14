//go:build !linux

package network

func TuneInterface(name string, mtu int, queueLength ...int) error { return nil }
