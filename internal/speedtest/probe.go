package speedtest

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

const (
	DefaultBytes     int64 = 5_000_000 // 每个方向默认传输 5 MB
	MaxBytes         int64 = 50_000_000
	MinBytes         int64 = 1_000_000
	DefaultTimeout         = 45 * time.Second
	directionTimeout       = 20 * time.Second
	downloadURL            = "https://speed.cloudflare.com/__down"
	uploadURL              = "https://speed.cloudflare.com/__up"
)

type TransferResult struct {
	OK         bool    `json:"ok"`
	Bytes      int64   `json:"bytes"`
	DurationMs int64   `json:"duration_ms"`
	SpeedBps   float64 `json:"speed_bps"`
	SpeedMbps  float64 `json:"speed_mbps"`
	Error      string  `json:"error,omitempty"`
}

// Result 包含独立的上下行结果；旧速度字段保留为下载结果的兼容别名。
type Result struct {
	OK         bool           `json:"ok"`
	NodeID     string         `json:"node_id"`
	Name       string         `json:"name"`
	Addr       string         `json:"addr"`
	LatencyMs  int64          `json:"latency_ms"`
	Download   TransferResult `json:"download"`
	Upload     TransferResult `json:"upload"`
	Bytes      int64          `json:"bytes"`
	DurationMs int64          `json:"duration_ms"`
	SpeedBps   float64        `json:"speed_bps"`
	SpeedMbps  float64        `json:"speed_mbps"`
	Error      string         `json:"error,omitempty"`
}

// Probe 经同一 SOCKS5 节点分别测量下载和上传，每个方向独立限时。
func Probe(ctx context.Context, node model.SocksNode, wantBytes int64) Result {
	if _, _, err := net.SplitHostPort(node.Addr); err != nil {
		message := "invalid addr: " + err.Error()
		return Result{NodeID: node.ID, Name: node.Name, Addr: node.Addr, Error: message,
			Download: TransferResult{Error: message}, Upload: TransferResult{Error: message}}
	}
	var latency atomic.Int64
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			start := time.Now()
			conn, err := dialSOCKS5(ctx, node.Addr, node.Username, node.Password, addr)
			if err == nil {
				latency.CompareAndSwap(0, int64(time.Since(start)))
			}
			return conn, err
		},
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives:     true,
		DisableCompression:    true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
	defer transport.CloseIdleConnections()
	result := probeDirections(ctx, node, wantBytes, &http.Client{Transport: transport}, downloadURL, uploadURL)
	result.LatencyMs = time.Duration(latency.Load()).Milliseconds()
	return result
}

func probeDirections(ctx context.Context, node model.SocksNode, size int64, client *http.Client, downURL, upURL string) Result {
	if size <= 0 {
		size = DefaultBytes
	}
	size = max(MinBytes, min(MaxBytes, size))
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	result := Result{NodeID: node.ID, Name: node.Name, Addr: node.Addr}
	downCtx, cancelDown := context.WithTimeout(ctx, directionTimeout)
	result.Download = download(downCtx, client, fmt.Sprintf("%s?bytes=%d", downURL, size), size)
	cancelDown()
	upCtx, cancelUp := context.WithTimeout(ctx, directionTimeout)
	result.Upload = upload(upCtx, client, upURL, size)
	cancelUp()
	result.OK = result.Download.OK && result.Upload.OK
	result.Bytes, result.DurationMs = result.Download.Bytes, result.Download.DurationMs
	result.SpeedBps, result.SpeedMbps = result.Download.SpeedBps, result.Download.SpeedMbps
	var failures []string
	if !result.Download.OK {
		failures = append(failures, "download: "+result.Download.Error)
	}
	if !result.Upload.OK {
		failures = append(failures, "upload: "+result.Upload.Error)
	}
	result.Error = strings.Join(failures, "; ")
	return result
}

func finishTransfer(result TransferResult, duration time.Duration, want int64) TransferResult {
	result.DurationMs = max(1, duration.Milliseconds())
	if result.Bytes != want {
		result.Error = fmt.Sprintf("incomplete transfer: %d of %d bytes", result.Bytes, want)
		return result
	}
	result.SpeedBps = float64(result.Bytes) / max(duration.Seconds(), 0.001)
	result.SpeedMbps = result.SpeedBps * 8 / 1_000_000
	result.OK = true
	return result
}

func download(ctx context.Context, client *http.Client, url string, size int64) TransferResult {
	var result TransferResult
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	request.Header.Set("User-Agent", "s2l-speedtest/1.1")
	response, err := client.Do(request)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Error = fmt.Sprintf("http status %d", response.StatusCode)
		return result
	}
	start := time.Now()
	result.Bytes, err = io.Copy(io.Discard, io.LimitReader(response.Body, size))
	if err != nil {
		result.Error = "read body: " + err.Error()
		return result
	}
	return finishTransfer(result, time.Since(start), size)
}

// uploadBody 生成固定大小的零字节流，并同步记录请求体与写入时间；无需大块内存。
type uploadBody struct {
	mu         sync.Mutex
	remaining  int64
	bytes      int64
	start, end time.Time
	writeErr   error
}

func (body *uploadBody) Read(buffer []byte) (int, error) {
	body.mu.Lock()
	defer body.mu.Unlock()
	if body.remaining == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(buffer)), body.remaining)
	clear(buffer[:n])
	body.remaining -= n
	body.bytes += n
	return int(n), nil
}

func upload(ctx context.Context, client *http.Client, url string, size int64) TransferResult {
	var result TransferResult
	body := &uploadBody{remaining: size}
	trace := &httptrace.ClientTrace{
		WroteHeaders: func() {
			body.mu.Lock()
			body.start = time.Now()
			body.mu.Unlock()
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			body.mu.Lock()
			body.end = time.Now()
			body.writeErr = info.Err
			body.mu.Unlock()
		},
	}
	request, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodPost, url, body)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	request.ContentLength = size
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("User-Agent", "s2l-speedtest/1.1")
	response, err := client.Do(request)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Error = fmt.Sprintf("http status %d", response.StatusCode)
		return result
	}
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10)); err != nil {
		result.Error = "upload response: " + err.Error()
		return result
	}
	body.mu.Lock()
	defer body.mu.Unlock()
	result.Bytes = body.bytes
	if body.writeErr != nil {
		result.Error = "write body: " + body.writeErr.Error()
		return result
	}
	if body.start.IsZero() || body.end.Before(body.start) {
		result.Error = "upload was not completed"
		return result
	}
	return finishTransfer(result, body.end.Sub(body.start), size)
}
