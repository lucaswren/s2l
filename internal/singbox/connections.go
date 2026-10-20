package singbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

type Connection struct {
	ID       string    `json:"id"`
	Upload   uint64    `json:"upload"`
	Download uint64    `json:"download"`
	Start    time.Time `json:"start"`
	Metadata struct {
		Network         string `json:"network"`
		SourceIP        string `json:"sourceIP"`
		DestinationIP   string `json:"destinationIP"`
		DestinationPort string `json:"destinationPort"`
		Host            string `json:"host"`
	} `json:"metadata"`
}
type Connections struct {
	Rows      []Connection `json:"rows"`
	Truncated bool         `json:"truncated"`
}

func prepareAPI(path string) (string, string, error) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return "", "", e
	}
	address := l.Addr().String()
	l.Close()
	var key [24]byte
	if _, e = rand.Read(key[:]); e != nil {
		return "", "", e
	}
	secret := hex.EncodeToString(key[:])
	data, e := os.ReadFile(path)
	if e != nil {
		return "", "", e
	}
	var cfg map[string]any
	if e = json.Unmarshal(data, &cfg); e != nil {
		return "", "", e
	}
	cfg["experimental"] = map[string]any{"clash_api": map[string]any{"external_controller": address, "secret": secret}}
	data, e = json.MarshalIndent(cfg, "", "  ")
	if e == nil {
		e = os.WriteFile(path, data, 0600)
	}
	return address, secret, e
}
func queryAPI(ctx context.Context, address, secret, method, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	r, e := http.NewRequestWithContext(ctx, method, "http://"+address+path, nil)
	if e != nil {
		return nil, e
	}
	r.Header.Set("Authorization", "Bearer "+secret)
	tr := &http.Transport{Proxy: nil, MaxResponseHeaderBytes: 8192}
	defer tr.CloseIdleConnections()
	c := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := c.Do(r)
	if e != nil {
		return nil, fmt.Errorf("连接观测接口不可用")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("连接观测接口返回 HTTP %d", res.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
	if e != nil || len(b) > 2<<20 {
		return nil, fmt.Errorf("连接列表超过读取限制")
	}
	return b, nil
}
func readConnectionRows(ctx context.Context, address, secret string) ([]Connection, error) {
	data, e := queryAPI(ctx, address, secret, "GET", "/connections")
	if e != nil {
		return nil, e
	}
	var r struct {
		Connections []Connection `json:"connections"`
	}
	if e = json.Unmarshal(data, &r); e != nil {
		return nil, fmt.Errorf("连接观测数据无效")
	}
	return r.Connections, nil
}
func readConnections(ctx context.Context, address, secret string) (Connections, error) {
	rows, e := readConnectionRows(ctx, address, secret)
	if e != nil {
		return Connections{}, e
	}
	out := Connections{Rows: rows}
	if len(out.Rows) > 500 {
		out.Rows = out.Rows[:500]
		out.Truncated = true
	}
	if out.Rows == nil {
		out.Rows = []Connection{}
	}
	return out, nil
}
func closeConnection(ctx context.Context, address, secret, id string) error {
	rows, e := readConnectionRows(ctx, address, secret)
	if e != nil {
		return e
	}
	for _, r := range rows {
		if r.ID == id {
			_, e = queryAPI(ctx, address, secret, "DELETE", "/connections/"+url.PathEscape(id))
			return e
		}
	}
	return fmt.Errorf("连接已结束或不在当前列表，请刷新")
}
