package dnsproxy

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

type Server struct {
	Resolver *Resolver
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	once     sync.Once
}

// Only sing-box connects here over loopback TCP; no public DNS listener.
func Start(node model.SocksNode, mode string) (*Server, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{Resolver: New(node, mode), listener: l, ctx: ctx, cancel: cancel}
	s.wg.Add(1)
	go s.serve()
	return s, nil
}
func (s *Server) Address() string { return s.listener.Addr().String() }
func (s *Server) Close() {
	s.once.Do(func() { s.cancel(); s.listener.Close(); s.wg.Wait(); s.Resolver.Close() })
}
func (s *Server) serve() {
	defer s.wg.Done()
	slots := make(chan struct{}, 64)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-slots }()
			defer conn.Close()
			stop := context.AfterFunc(s.ctx, func() { conn.Close() })
			defer stop()
			for {
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				var size [2]byte
				if _, err := io.ReadFull(conn, size[:]); err != nil {
					return
				}
				n := binary.BigEndian.Uint16(size[:])
				if n < 12 {
					return
				}
				q := make([]byte, n)
				if _, err := io.ReadFull(conn, q); err != nil {
					return
				}
				ctx, cancel := context.WithTimeout(s.ctx, 6*time.Second)
				answer, err := s.Resolver.Resolve(ctx, q)
				cancel()
				if err != nil {
					answer = append([]byte(nil), q...)
					answer[2] = (q[2] & 1) | 0x80
					answer[3] = (q[3] & 0x10) | 0x82
					for i := 6; i < 12; i++ {
						answer[i] = 0
					}
					questionBytes, e := question(q)
					if e != nil {
						return
					}
					answer = answer[:12+len(questionBytes)]
				}
				binary.BigEndian.PutUint16(size[:], uint16(len(answer)))
				if _, err := conn.Write(size[:]); err != nil {
					return
				}
				if _, err := conn.Write(answer); err != nil {
					return
				}
			}
		}()
	}
}
