package speedtest

import (
	"context"
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sort"
	"sync"
	"time"
)

type TransferSample struct {
	Requested   int     `json:"requested"`
	Received    int     `json:"received"`
	FirstByteMS float64 `json:"first_byte_ms"`
	DurationMS  float64 `json:"duration_ms"`
	Status      int     `json:"status"`
	Complete    bool    `json:"complete"`
	Bounded     bool    `json:"bounded"`
	Error       string  `json:"error,omitempty"`
}
type Quality struct {
	Samples   []TransferSample `json:"samples"`
	MedianMS  float64          `json:"median_ms"`
	P95MS     float64          `json:"p95_ms"`
	Successes int              `json:"successes"`
	Scope     string           `json:"scope"`
}

func ProbeQuality(ctx context.Context, node model.SocksNode, target string) Quality {
	return probeQuality(ctx, node, "https://"+target+"/")
}
func probeQuality(ctx context.Context, node model.SocksNode, target string) Quality {
	out := Quality{Samples: []TransferSample{}, Scope: "经固定 SOCKS5 请求目标；Range 可能被目标忽略，达到读取上限不代表完整下载或 MTU 已验证"}
	values := []float64{}
	for i := 0; i < 8 && ctx.Err() == nil; i++ {
		size := 1024
		if i%2 == 1 {
			size = 256 * 1024
		}
		s := transferSample(ctx, node, target, size)
		out.Samples = append(out.Samples, s)
		if s.Error == "" && s.Status >= 200 && s.Status < 300 {
			out.Successes++
			values = append(values, s.DurationMS)
		}
	}
	if len(values) > 0 {
		sort.Float64s(values)
		out.MedianMS = values[len(values)/2]
		if len(values)%2 == 0 {
			out.MedianMS = (values[len(values)/2-1] + out.MedianMS) / 2
		}
		idx := (95*len(values)+99)/100 - 1
		out.P95MS = values[idx]
	}
	return out
}
func transferSample(ctx context.Context, node model.SocksNode, target string, size int) TransferSample {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s := TransferSample{Requested: size}
	var mu sync.Mutex
	first := 0.0
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotFirstResponseByte: func() { mu.Lock(); first = float64(time.Since(start).Microseconds()) / 1000; mu.Unlock() }})
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 32768, DialContext: func(c context.Context, _, addr string) (net.Conn, error) {
		return DialSOCKS5(c, node.Addr, node.Username, node.Password, addr)
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, "GET", target, nil)
	if e == nil {
		req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", size-1))
		var res *http.Response
		res, e = client.Do(req)
		if e == nil {
			s.Status = res.StatusCode
			var data []byte
			data, e = io.ReadAll(io.LimitReader(res.Body, int64(size+1)))
			res.Body.Close()
			s.Received = len(data)
			s.Bounded = len(data) > size
			s.Complete = e == nil && !s.Bounded
		}
	}
	if e != nil {
		s.Error = "传输失败、提前断开或超时"
	}
	mu.Lock()
	s.FirstByteMS = first
	mu.Unlock()
	s.DurationMS = float64(time.Since(start).Microseconds()) / 1000
	return s
}
