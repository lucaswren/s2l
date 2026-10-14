package speedtest

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func dnsAnswer(query []byte) []byte {
	response := append([]byte(nil), query...)
	response[2] = 0x81
	response[3] = 0x80
	response[6] = 0
	response[7] = 1
	return append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 93, 184, 216, 34)
}

func TestDNSResponseValidation(t *testing.T) {
	q, err := dnsQuery()
	if err != nil {
		t.Fatal(err)
	}
	good := dnsAnswer(q)
	if err := validateDNS(q, good); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"id", "nxdomain", "question", "no-answer", "truncated", "pointer-loop", "not-response", "truncated-flag", "foreign-owner"} {
		t.Run(kind, func(t *testing.T) {
			r := append([]byte(nil), good...)
			switch kind {
			case "id":
				r[0] ^= 1
			case "nxdomain":
				r[3] = 0x83
			case "question":
				r[13] = 'z'
			case "no-answer":
				r[7] = 0
			case "truncated":
				r = r[:len(r)-1]
			case "pointer-loop":
				r[len(q)] = 0xc0
				r[len(q)+1] = byte(len(q))
			case "not-response":
				r[2] = 1
			case "truncated-flag":
				r[2] |= 2
			case "foreign-owner":
				r = append(append([]byte(nil), good[:len(q)]...), 3, 'b', 'a', 'd', 0, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 93, 184, 216, 34) // pointer resolves to ID bytes, not the question owner
			}
			if validateDNS(q, r) == nil {
				t.Fatal("invalid DNS response accepted")
			}
		})
	}
}

// Keep all fixture goroutines and sockets owned by the test.
func socksFixture(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	var wg sync.WaitGroup
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				serve(conn)
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-acceptDone
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return listener.Addr().String()
}

func fixtureGreeting(conn net.Conn, auth bool) bool {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return false
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return false
	}
	if !auth {
		_, err := conn.Write([]byte{5, 0})
		return err == nil
	}
	conn.Write([]byte{5, 2})
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return false
	}
	user := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, user); err != nil {
		return false
	}
	var length [1]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return false
	}
	password := make([]byte, int(length[0]))
	if _, err := io.ReadFull(conn, password); err != nil {
		return false
	}
	if string(user) != "user" || string(password) != "secret" {
		conn.Write([]byte{1, 1})
		return false
	}
	conn.Write([]byte{1, 0})
	return true
}

func TestUDPHealthThroughAuthenticatedSOCKSRelay(t *testing.T) {
	for _, mode := range []string{"valid", "unsupported", "refused", "wrong-id", "fragment", "wrong-source", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				buffer := make([]byte, 4096)
				for {
					n, peer, err := relay.ReadFromUDP(buffer)
					if err != nil {
						return
					}
					if n < 10 || buffer[0] != 0 || buffer[1] != 0 || buffer[2] != 0 || buffer[3] != 1 {
						t.Error("probe sent a raw or invalid DNS datagram")
						return
					}
					target := net.JoinHostPort(net.IP(buffer[4:8]).String(), strconv.Itoa(int(binary.BigEndian.Uint16(buffer[8:10]))))
					if target != "1.1.1.1:53" && target != "8.8.8.8:53" {
						t.Error("unexpected UDP destination")
						return
					}
					if mode == "timeout" {
						continue
					}
					dns := dnsAnswer(buffer[10:n])
					if mode == "wrong-id" {
						dns[0] ^= 1
					}
					if mode == "wrong-source" {
						if target == "1.1.1.1:53" {
							target = "8.8.8.8:53"
						} else {
							target = "1.1.1.1:53"
						}
					}
					reply, _ := udpRequest(target, dns)
					if mode == "fragment" {
						reply[2] = 1
					}
					relay.WriteToUDP(reply, peer)
				}
			}()
			t.Cleanup(func() { relay.Close(); <-done })
			proxy := socksFixture(t, func(conn net.Conn) {
				if !fixtureGreeting(conn, true) {
					return
				}
				var request [4]byte
				if _, err := io.ReadFull(conn, request[:]); err != nil {
					return
				}
				target, err := readSOCKSAddress(conn, request[3])
				if err != nil || request[1] != 3 || target != "0.0.0.0:0" {
					t.Error("expected UDP ASSOCIATE")
					return
				}
				if mode == "unsupported" || mode == "refused" {
					code := byte(7)
					if mode == "refused" {
						code = 2
					}
					conn.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				// Wildcard relay addresses must use the proxy peer, never a DNS destination.
				port := relay.LocalAddr().(*net.UDPAddr).Port
				conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, byte(port >> 8), byte(port)})
				io.Copy(io.Discard, conn)
			})
			timeout := 200 * time.Millisecond
			if mode == "valid" {
				timeout = time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			result := checkUDPHealth(ctx, model.SocksNode{Addr: proxy, Username: "user", Password: "secret"})
			want := "unavailable"
			if mode == "valid" {
				want = "healthy"
			}
			if mode == "unsupported" {
				want = "unsupported"
			}
			if result.State != want {
				t.Fatalf("got %+v, want %s", result, want)
			}
		})
	}
}

func connectFixture(t *testing.T) string {
	return socksFixture(t, func(conn net.Conn) {
		if !fixtureGreeting(conn, false) {
			return
		}
		var request [4]byte
		if _, err := io.ReadFull(conn, request[:]); err != nil {
			return
		}
		target, err := readSOCKSAddress(conn, request[3])
		if err != nil || request[1] != 1 {
			return
		}
		host, _, _ := net.SplitHostPort(target)
		if host != "127.0.0.1" {
			t.Error("fixture attempted an external connection")
			return
		}
		upstream, err := net.Dial("tcp", target)
		if err != nil {
			return
		}
		defer upstream.Close()
		conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
		copyDone := make(chan struct{})
		go func() { io.Copy(upstream, conn); close(copyDone) }()
		io.Copy(conn, upstream)
		conn.Close()
		upstream.Close()
		<-copyDone
	})
}

func TestDNSDoHAndTCPFallbackThroughProxy(t *testing.T) {
	for _, mode := range []string{"doh", "fallback", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			doh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/dns-message" {
					t.Error("invalid DoH request")
				}
				if mode != "doh" {
					w.WriteHeader(503)
					return
				}
				q, _ := io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/dns-message")
				w.Write(dnsAnswer(q))
			}))
			defer doh.Close()
			tcp := socksFixture(t, func(conn net.Conn) {
				var size [2]byte
				if _, err := io.ReadFull(conn, size[:]); err != nil {
					return
				}
				q := make([]byte, int(binary.BigEndian.Uint16(size[:])))
				if _, err := io.ReadFull(conn, q); err != nil {
					return
				}
				r := dnsAnswer(q)
				if mode == "invalid" {
					r[0] ^= 1
				}
				binary.BigEndian.PutUint16(size[:], uint16(len(r)))
				conn.Write(size[:])
				conn.Write(r)
			})
			proxy := connectFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result := checkDNSHealthTargets(ctx, model.SocksNode{Addr: proxy}, doh.URL, tcp)
			want := "healthy"
			method := "doh"
			if mode == "fallback" {
				want = "degraded"
				method = "tcp-fallback"
			}
			if mode == "invalid" {
				want = "unavailable"
				method = "doh/tcp"
			}
			if result.State != want || result.Method != method {
				t.Fatalf("got %+v, want %s/%s", result, want, method)
			}
		})
	}
}

func TestDNSHealthRespectsForcedResolverMode(t *testing.T) {
	for _, mode := range []string{"tcp", "doh"} {
		t.Run(mode, func(t *testing.T) {
			var dohCalls, tcpCalls atomic.Int32
			doh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { dohCalls.Add(1); w.WriteHeader(503) }))
			defer doh.Close()
			tcp := socksFixture(t, func(conn net.Conn) {
				tcpCalls.Add(1)
				var size [2]byte
				if _, err := io.ReadFull(conn, size[:]); err != nil {
					return
				}
				q := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, err := io.ReadFull(conn, q); err != nil {
					return
				}
				a := dnsAnswer(q)
				binary.BigEndian.PutUint16(size[:], uint16(len(a)))
				conn.Write(append(size[:], a...))
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result := checkDNSHealthTargets(ctx, model.SocksNode{Addr: connectFixture(t)}, doh.URL, tcp, mode)
			if mode == "tcp" {
				if result.State != "healthy" || result.Method != "tcp" || dohCalls.Load() != 0 || tcpCalls.Load() != 1 {
					t.Fatal("TCP-only mode probed DoH", result)
				}
			} else if result.State != "unavailable" || result.Method != "doh" || dohCalls.Load() != 1 || tcpCalls.Load() != 0 {
				t.Fatal("DoH-only mode silently used TCP fallback", result)
			}
		})
	}
}

func TestDNSCompressedCNAMEChain(t *testing.T) {
	q, err := dnsQuery()
	if err != nil {
		t.Fatal(err)
	}
	response := append([]byte(nil), q...)
	response[2] = 0x81
	response[3] = 0x80
	response[7] = 2
	alias := []byte{5, 'a', 'l', 'i', 'a', 's', 0xc0, 0x0c}
	response = append(response, 0xc0, 0x0c, 0, 5, 0, 1, 0, 0, 0, 60, 0, byte(len(alias)))
	offset := len(response)
	response = append(response, alias...)
	response = append(response, 0xc0|byte(offset>>8), byte(offset), 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 93, 184, 216, 34)
	if err := validateDNS(q, response); err != nil {
		t.Fatal(err)
	}
}

func FuzzDNSResponseValidation(f *testing.F) {
	q, _ := dnsQuery()
	q[0], q[1] = 0x12, 0x34 // Stable across fuzz worker processes; production IDs stay random.
	f.Add(dnsAnswer(q))
	f.Add([]byte{})
	f.Add([]byte{0xc0, 0})
	f.Fuzz(func(t *testing.T, response []byte) {
		if len(response) > 65535 {
			t.Skip()
		}
		_ = validateDNS(q, response)
	})
}
