package network

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// run 执行系统命令，失败时返回合并后的 stderr/stdout。
func run(name string, args ...string) error {
	return runContext(context.Background(), name, args...)
}

func runContext(parent context.Context, name string, args ...string) error {
	if name == "iptables" || name == "ip6tables" {
		args = append([]string{"-w", "5"}, args...)
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s %s: %s: %w", name, strings.Join(args, " "), msg, err)
	}
	return nil
}

// output 执行命令并返回 stdout。
func output(name string, args ...string) (string, error) {
	return outputContext(context.Background(), name, args...)
}

func outputContext(parent context.Context, name string, args ...string) (string, error) {
	if name == "iptables" || name == "ip6tables" {
		args = append([]string{"-w", "5"}, args...)
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s %s: %s: %w", name, strings.Join(args, " "), msg, err)
	}
	return stdout.String(), nil
}
