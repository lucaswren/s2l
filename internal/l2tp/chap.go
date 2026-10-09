package l2tp

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode"

	"github.com/lucaswren/s2l/internal/model"
)

const markerPrefix = "# s2l:"

// ChapManager 管理 /etc/ppp/chap-secrets 中由本程序维护的账号行
type ChapManager struct {
	path string
	mu   sync.Mutex
}

func NewChapManager(path string) *ChapManager {
	if path == "" {
		path = "/etc/ppp/chap-secrets"
	}
	return &ChapManager{path: path}
}

// Upsert 写入或更新一条 L2TP 账号：
//
//	"<L2TPUser>" * "<L2TPPass>" "<ClientIP>"
func (c *ChapManager) Upsert(m model.TunnelMapping) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	clientIP, err := ClientIPFromSubnet(m.Subnet)
	if err != nil {
		return err
	}
	if err := validateSecret(m.L2TPUser); err != nil {
		return fmt.Errorf("invalid L2TP username: %w", err)
	}
	if err := validateSecret(m.L2TPPass); err != nil {
		return fmt.Errorf("invalid L2TP password: %w", err)
	}
	line := fmt.Sprintf("%s%s\n\"%s\" * \"%s\" %s\n",
		markerPrefix, m.ID,
		escapeQuotes(m.L2TPUser), escapeQuotes(m.L2TPPass), clientIP,
	)
	return c.rewrite(func(lines []string) []string {
		out := removeMarked(lines, m.ID)
		return append(out, strings.TrimRight(line, "\n"))
	})
}

// Remove 删除指定映射对应的 chap-secrets 条目
func (c *ChapManager) Remove(mappingID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rewrite(func(lines []string) []string {
		return removeMarked(lines, mappingID)
	})
}

func (c *ChapManager) rewrite(mutate func([]string) []string) error {
	var lines []string
	f, err := os.Open(c.path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		// 文件不存在则创建
		lines = []string{
			"# Secrets for authentication using CHAP",
			"# client        server  secret                  IP addresses",
		}
	} else {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		_ = f.Close()
		if err := sc.Err(); err != nil {
			return err
		}
	}

	lines = mutate(lines)
	content := strings.Join(lines, "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	tmp := c.path + ".s2l.tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// removeMarked 删除 marker + 紧随其后的账号行
func removeMarked(lines []string, mappingID string) []string {
	marker := markerPrefix + mappingID
	out := make([]string, 0, len(lines))
	skipNext := false
	for _, line := range lines {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.TrimSpace(line) == marker {
			skipNext = true
			continue
		}
		out = append(out, line)
	}
	return out
}

func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, `"`, ``)
}

func validateSecret(s string) error {
	if s == "" || len(s) > 255 {
		return fmt.Errorf("must contain 1-255 bytes")
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == '"' || r == '\\' {
			return fmt.Errorf("contains unsupported characters")
		}
	}
	return nil
}
