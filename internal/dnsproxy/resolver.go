// Package dnsproxy provides DNS failover within each mapping's selected egress.
package dnsproxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/speedtest"
)

type Resolver struct {
	mu                  sync.Mutex
	status              model.ResolverStatus
	failures, successes int
	nextProbe           time.Time
	probing             bool
	exchange            func(context.Context, string, []byte) ([]byte, error)
	dohEndpoint         string
	tcpEndpoint         string
	transport           *http.Transport
	tcp                 *tcpPool
	ctx                 context.Context
	cancel              context.CancelFunc
	closed              bool
	wg                  sync.WaitGroup
}

func New(node model.SocksNode, options model.DNSOptions) *Resolver {
	options = options.WithDefaults()
	return newResolver(node, options.Mode, options.DoHURL, options.TCPAddress)
}

func newResolver(node model.SocksNode, mode, dohURL, tcpAddress string) *Resolver {
	if mode == "" {
		mode = "auto"
	}
	r := &Resolver{status: model.ResolverStatus{Mode: mode, Active: "doh"}}
	if endpoint, err := url.Parse(dohURL); err == nil {
		r.dohEndpoint = endpoint.Host
	}
	r.tcpEndpoint = tcpAddress
	r.ctx, r.cancel = context.WithCancel(context.Background())
	if mode == "tcp" {
		r.status.Active = "tcp"
	}
	r.transport = &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, MaxConnsPerHost: 8, MaxIdleConns: 4, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, MaxResponseHeaderBytes: 64 * 1024,
		TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 3 * time.Second,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return speedtest.DialNode(ctx, node, addr)
		}}
	r.tcp = &tcpPool{slots: make(chan struct{}, 4), dial: func(ctx context.Context) (net.Conn, error) {
		return speedtest.DialNode(ctx, node, tcpAddress)
	}}
	r.exchange = func(ctx context.Context, method string, query []byte) ([]byte, error) {
		if method == "doh" {
			req, err := http.NewRequestWithContext(ctx, "POST", dohURL, bytes.NewReader(query))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/dns-message")
			req.Header.Set("Accept", "application/dns-message")
			client := http.Client{Transport: r.transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := client.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
			if err != nil || media != "application/dns-message" || resp.StatusCode != 200 {
				return nil, &speedtest.StageError{Stage: "doh-http-response", Err: errors.New("invalid DoH response")}
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
			if len(body) > 65535 {
				return nil, &speedtest.StageError{Stage: "dns-response", Err: errors.New("DNS response too large")}
			}
			return body, err
		}
		return r.tcp.query(ctx, query)
	}
	return r
}

func (r *Resolver) Status() model.ResolverStatus { r.mu.Lock(); defer r.mu.Unlock(); return r.status }
func (r *Resolver) Close() {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	r.mu.Unlock()
	r.wg.Wait()
	if r.tcp != nil {
		r.tcp.close()
	}
	if r.transport != nil {
		r.transport.CloseIdleConnections()
	}
}

// Responses must match the query; NXDOMAIN is valid, SERVFAIL/REFUSED trigger fallback.
func question(packet []byte) ([]byte, error) {
	if len(packet) < 12 || binary.BigEndian.Uint16(packet[4:6]) != 1 {
		return nil, errors.New("one DNS question required")
	}
	pos := 12
	for {
		if pos >= len(packet) {
			return nil, io.ErrUnexpectedEOF
		}
		n := int(packet[pos])
		pos++
		if n == 0 {
			break
		}
		if n > 63 || pos+n > len(packet) {
			return nil, errors.New("invalid DNS question name")
		}
		pos += n
		if pos > 266 {
			return nil, errors.New("DNS name too long")
		}
	}
	if pos+4 > len(packet) {
		return nil, io.ErrUnexpectedEOF
	}
	return packet[12 : pos+4], nil
}

func ValidateResponse(query, response []byte) error {
	q, err := question(query)
	if err != nil {
		return err
	}
	a, err := question(response)
	if err != nil {
		return err
	}
	if response[0] != query[0] || response[1] != query[1] || response[2]&0x80 == 0 || response[2]&0x02 != 0 || response[2]&0x78 != query[2]&0x78 || len(q) != len(a) || !bytes.EqualFold(q[:len(q)-4], a[:len(a)-4]) || !bytes.Equal(q[len(q)-4:], a[len(a)-4:]) {
		return errors.New("DNS response mismatch")
	}
	code, err := responseCode(response)
	if err != nil {
		return err
	}
	if code != 0 && code != 3 {
		return fmt.Errorf("DNS server returned failure (RCODE %d)", code)
	}
	return nil
}

func (r *Resolver) attempt(ctx context.Context, method string, query []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	r.mu.Lock()
	exchange := r.exchange
	r.mu.Unlock()
	response, err := exchange(ctx, method, query)
	stage := "exchange"
	if err == nil {
		stage = "dns-response"
		err = ValidateResponse(query, response)
	}
	if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
		var protocolError *speedtest.StageError
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			stage = "timeout"
		case errors.As(err, &protocolError):
			stage = protocolError.Stage
		}
		r.mu.Lock()
		failure := &model.DNSAttemptFailure{At: time.Now(), Stage: stage, SOCKSReply: speedtest.SOCKSReplyCode(err)}
		if method == "doh" {
			if failure.SOCKSReply != 0 {
				r.status.DoHProxyRejections++
			}
			failure.Endpoint = r.dohEndpoint
			r.status.LastDoHFailure = failure
		} else {
			if failure.SOCKSReply != 0 {
				r.status.TCPProxyRejections++
			}
			failure.Endpoint = r.tcpEndpoint
			r.status.LastTCPFailure = failure
		}
		r.mu.Unlock()
	}
	return response, err
}

// Start the alternate transport when the preferred one fails or stalls. Both
// still use this mapping's SOCKS5 exit. A buffered result channel and a child
// context let the first valid answer win without leaving a blocked sender.
const fallbackDelay = 300 * time.Millisecond

func (r *Resolver) autoAttempt(ctx context.Context, preferred string, query []byte) ([]byte, string, bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		answer []byte
		method string
		err    error
	}
	results := make(chan result, 2)
	launch := func(method string) {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			results <- result{method: method, err: context.Canceled}
			return
		}
		r.wg.Add(1)
		r.mu.Unlock()
		q := append([]byte(nil), query...)
		go func() {
			defer r.wg.Done()
			a, err := r.attempt(ctx, method, q)
			results <- result{answer: a, method: method, err: err}
		}()
	}
	alternate := "tcp"
	if preferred == "tcp" {
		alternate = "doh"
	}
	launch(preferred)
	timer := time.NewTimer(fallbackDelay)
	defer timer.Stop()
	pending, startedBackup := 1, false
	preferredFailed := false
	startBackup := func() {
		if !startedBackup {
			startedBackup = true
			pending++
			launch(alternate)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil, "", false, ctx.Err()
		case <-timer.C:
			startBackup()
		case v := <-results:
			pending--
			if v.err == nil {
				return v.answer, v.method, preferredFailed, nil
			}
			if v.method == preferred {
				preferredFailed = true
			}
			startBackup()
			if pending == 0 {
				return nil, "", preferredFailed, v.err
			}
		}
	}
}

func (r *Resolver) Resolve(ctx context.Context, query []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := question(query); err != nil {
		return nil, err
	}
	if query[2]&0xf8 != 0 {
		return nil, errors.New("only standard DNS queries accepted")
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errors.New("DNS resolver closed")
	}
	r.status.Queries++
	mode, active, generation := r.status.Mode, r.status.Active, r.status.Switches
	probe := mode == "auto" && active == "tcp" && !r.probing && !time.Now().Before(r.nextProbe)
	if probe {
		r.probing = true
		r.wg.Add(1)
	}
	r.mu.Unlock()
	if probe {
		go r.probePrimary(append([]byte(nil), query...), generation)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	method := active
	if mode == "doh" {
		method = "doh"
	}
	var response []byte
	var err error
	used := method
	var preferredFailed bool
	if mode == "auto" {
		response, used, preferredFailed, err = r.autoAttempt(ctx, method, query)
	} else {
		response, err = r.attempt(ctx, method, query)
	}
	// Caller cancellation is not an upstream fault and must not switch resolvers.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if r.ctx.Err() != nil {
		return nil, r.ctx.Err()
	}
	r.mu.Lock()
	if !r.closed && method == "doh" && mode == "auto" && generation == r.status.Switches {
		switch {
		case err != nil:
			// Neither transport answered, so there is no usable backup to prefer.
			r.failures = 0
		case preferredFailed:
			r.notePrimaryFailure()
		case used == "doh" && err == nil:
			r.failures = 0
		case used == "tcp" && err == nil && !r.probing && !time.Now().Before(r.nextProbe):
			// A faster backup leaves the primary result unknown. Confirm it
			// independently at a bounded rate without delaying the client.
			r.probing = true
			r.wg.Add(1)
			go r.probePrimary(append([]byte(nil), query...), generation)
		}
	}
	r.mu.Unlock()
	if err == nil && mode == "auto" && used != method {
		r.mu.Lock()
		r.status.Fallbacks++
		r.mu.Unlock()
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r.mu.Lock()
		r.status.Failures++
		r.mu.Unlock()
		return nil, fmt.Errorf("DNS forwarding failed")
	}
	return response, nil
}

// notePrimaryFailure is called with mu held.
func (r *Resolver) notePrimaryFailure() {
	r.failures++
	if r.failures >= 2 {
		r.status.Active = "tcp"
		r.status.Switches++
		r.successes = 0
		r.nextProbe = time.Now().Add(60 * time.Second)
	}
}

// Recheck the primary in the background; an available backup must not wait for it.
func (r *Resolver) probePrimary(query []byte, generation uint64) {
	defer r.wg.Done()
	_, err := r.attempt(r.ctx, "doh", query)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probing = false
	if r.closed || r.ctx.Err() != nil || generation != r.status.Switches {
		return
	}
	r.nextProbe = time.Now().Add(30 * time.Second)
	if r.status.Active == "doh" {
		if err != nil {
			r.notePrimaryFailure()
		} else {
			r.failures = 0
		}
		return
	}
	if err != nil {
		r.successes = 0
		return
	}
	r.successes++
	if r.successes >= 2 && r.status.Active == "tcp" {
		r.status.Active = "doh"
		r.status.Switches++
		r.failures = 0
		r.successes = 0
	}
}
