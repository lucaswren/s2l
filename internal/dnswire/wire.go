// Package dnswire validates DNS messages shared by forwarding and health probes.
package dnswire

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

func QueryA(host string) ([]byte, error) {
	host = strings.TrimSuffix(host, ".")
	if len(host) == 0 || len(host) > 253 {
		return nil, errors.New("invalid DNS name")
	}
	q := make([]byte, 12)
	if _, err := rand.Read(q[:2]); err != nil {
		return nil, err
	}
	q[2] = 1
	q[5] = 1
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 {
			return nil, errors.New("invalid DNS label")
		}
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	return append(q, 0, 0, 1, 0, 1), nil
}
func SkipName(packet []byte, pos int) (int, error) {
	return walkName(packet, pos, nil)
}

func Name(packet []byte, pos int) (string, int, error) {
	var labels []string
	end, err := walkName(packet, pos, &labels)
	if err != nil {
		return "", 0, err
	}
	return strings.Join(labels, "."), end, nil
}

func walkName(packet []byte, pos int, labels *[]string) (int, error) {
	end, expanded := -1, 0
	for steps := 0; steps < 128; steps++ {
		if pos < 0 || pos >= len(packet) {
			break
		}
		n := int(packet[pos])
		pos++
		if n == 0 {
			if end >= 0 {
				return end, nil
			}
			return pos, nil
		}
		if n&0xc0 == 0xc0 {
			if pos >= len(packet) {
				break
			}
			if end < 0 {
				end = pos + 1
			}
			pos = (n&0x3f)<<8 | int(packet[pos])
			continue
		}
		if n > 63 || pos+n > len(packet) {
			break
		}
		expanded += n + 1
		if expanded > 254 {
			break
		}
		if labels != nil {
			*labels = append(*labels, string(packet[pos:pos+n]))
		}
		pos += n
	}
	return 0, errors.New("invalid DNS name")
}

// Question returns the wire name, type and class of an uncompressed question.
func Question(packet []byte) ([]byte, error) {
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

// ValidateResponse accepts successful replies and NXDOMAIN for the query.
func ValidateResponse(query, response []byte) error {
	q, err := Question(query)
	if err != nil {
		return err
	}
	a, err := Question(response)
	if err != nil {
		return err
	}
	if response[0] != query[0] || response[1] != query[1] || response[2]&0x80 == 0 || response[2]&0x02 != 0 || response[2]&0x78 != query[2]&0x78 || !equalName(q[:len(q)-4], a[:len(a)-4]) || !bytes.Equal(q[len(q)-4:], a[len(a)-4:]) {
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

// DNS case folding applies only to ASCII letters, even in binary labels.
func equalName(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	lower := func(c byte) byte {
		if 'A' <= c && c <= 'Z' {
			return c + ('a' - 'A')
		}
		return c
	}
	for i := range a {
		if lower(a[i]) != lower(b[i]) {
			return false
		}
	}
	return true
}

func responseCode(packet []byte) (int, error) {
	pos, err := SkipName(packet, 12)
	if err != nil {
		return 0, err
	}
	pos += 4
	additionalStart := int(binary.BigEndian.Uint16(packet[6:8])) + int(binary.BigEndian.Uint16(packet[8:10]))
	count := additionalStart + int(binary.BigEndian.Uint16(packet[10:12]))
	code := int(packet[3] & 15)
	seenOPT := false
	for i := 0; i < count; i++ {
		nameStart := pos
		pos, err = SkipName(packet, pos)
		if err != nil || pos+10 > len(packet) {
			return 0, errors.New("invalid DNS record")
		}
		kind := binary.BigEndian.Uint16(packet[pos:])
		size := int(binary.BigEndian.Uint16(packet[pos+8:]))
		extendedCode := int(packet[pos+4])
		pos += 10
		end := pos + size
		if end > len(packet) {
			return 0, errors.New("truncated DNS record")
		}
		switch kind {
		case 41:
			if i < additionalStart || seenOPT || packet[nameStart] != 0 {
				return 0, errors.New("invalid DNS OPT record")
			}
			for option := pos; option < end; {
				if end-option < 4 {
					return 0, errors.New("truncated DNS OPT option")
				}
				size := int(binary.BigEndian.Uint16(packet[option+2:]))
				option += 4 + size
				if option > end {
					return 0, errors.New("truncated DNS OPT option data")
				}
			}
			seenOPT = true
			code |= extendedCode << 4
		case 1:
			if size != 4 {
				return 0, errors.New("invalid A record")
			}
		case 28:
			if size != 16 {
				return 0, errors.New("invalid AAAA record")
			}
		case 2, 5, 12, 39:
			nameEnd, e := SkipName(packet, pos)
			if e != nil || nameEnd != end {
				return 0, errors.New("invalid DNS name record")
			}
		case 6:
			next, e := SkipName(packet, pos)
			if e != nil || next > end {
				return 0, errors.New("invalid SOA record")
			}
			next, e = SkipName(packet, next)
			if e != nil || next+20 != end {
				return 0, errors.New("invalid SOA record")
			}
		}
		pos = end
	}
	if pos != len(packet) {
		return 0, errors.New("unexpected trailing DNS data")
	}
	return code, nil
}
