//go:build !linux

package store

func syncDirectory(string) error { return nil }
