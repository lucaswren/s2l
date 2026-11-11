package dnsproxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/speedtest"
)

var errTCPPoolWait = fmt.Errorf("DNS TCP connection pool wait: %w", context.DeadlineExceeded)

// Each leased connection processes one query at a time. The bounded pool is
// private to one mapping and its fixed SOCKS5 exit; DNS packets are not cached.
type tcpPool struct {
	mu     sync.Mutex
	idle   []net.Conn
	closed bool
	slots  chan struct{}
	dial   func(context.Context) (net.Conn, error)
}

func (p *tcpPool) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for _, c := range p.idle {
		c.Close()
	}
	p.idle = nil
}
func (p *tcpPool) query(ctx context.Context, q []byte) ([]byte, error) {
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	case <-ctx.Done():
	}
	if err := ctx.Err(); err != nil {
		if err == context.DeadlineExceeded {
			return nil, errTCPPoolWait
		}
		return nil, err
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, fmt.Errorf("DNS pool closed")
		}
		var c net.Conn
		if n := len(p.idle); n > 0 && attempt == 0 {
			c = p.idle[n-1]
			p.idle = p.idle[:n-1]
		}
		p.mu.Unlock()
		reused := c != nil
		var err error
		if c == nil {
			c, err = p.dial(ctx)
			if err != nil {
				return nil, err
			}
		}
		stop := context.AfterFunc(ctx, func() { c.Close() })
		deadline := time.Now().Add(3 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		err = c.SetDeadline(deadline)
		frame := make([]byte, 2+len(q))
		binary.BigEndian.PutUint16(frame, uint16(len(q)))
		copy(frame[2:], q)
		if err == nil {
			_, err = io.Copy(c, bytes.NewReader(frame))
		}
		var size [2]byte
		if err == nil {
			_, err = io.ReadFull(c, size[:])
		}
		var answer []byte
		if err == nil {
			answer = make([]byte, binary.BigEndian.Uint16(size[:]))
			_, err = io.ReadFull(c, answer)
		}
		stopped := stop()
		if err == nil && stopped && ctx.Err() == nil {
			if validationErr := ValidateResponse(q, answer); validationErr != nil {
				c.Close()
				return nil, &speedtest.StageError{Stage: "dns-response", Err: validationErr}
			}
			err = c.SetDeadline(time.Time{})
			p.mu.Lock()
			if err == nil && !p.closed {
				p.idle = append(p.idle, c)
			} else {
				c.Close()
			}
			p.mu.Unlock()
			// A valid response remains valid if the peer closes immediately
			// afterwards. Failure to reset its deadline only prevents reuse.
			return answer, nil
		}
		c.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		last = err
		if !reused {
			break
		} // Only replay an idempotent query after I/O fails on a reused socket.
	}
	if last == nil {
		last = context.Canceled
	}
	return nil, last
}
