package dnsproxy

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
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
func skipName(packet []byte, pos int) (int, error) {
	end := -1
	expanded := 0
	for steps := 0; steps < 128; steps++ {
		if pos >= len(packet) {
			break
		}
		n := int(packet[pos])
		pos++
		if n == 0 {
			if end != -1 {
				return end, nil
			}
			return pos, nil
		}
		if n&0xc0 == 0xc0 {
			if pos >= len(packet) {
				break
			}
			target := (n&0x3f)<<8 | int(packet[pos])
			if target >= len(packet) {
				break
			}
			if end == -1 {
				end = pos + 1
			}
			pos = target
			continue
		}
		if n > 63 || pos+n > len(packet) {
			break
		}
		pos += n
		expanded += n + 1
		if expanded > 255 {
			break
		}
	}
	return 0, errors.New("invalid DNS name")
}

func validateRecords(packet []byte) error {
	pos, err := skipName(packet, 12)
	if err != nil {
		return err
	}
	pos += 4
	count := int(binary.BigEndian.Uint16(packet[6:8])) + int(binary.BigEndian.Uint16(packet[8:10])) + int(binary.BigEndian.Uint16(packet[10:12]))
	for i := 0; i < count; i++ {
		pos, err = skipName(packet, pos)
		if err != nil || pos+10 > len(packet) {
			return errors.New("invalid DNS record")
		}
		kind := binary.BigEndian.Uint16(packet[pos:])
		size := int(binary.BigEndian.Uint16(packet[pos+8:]))
		pos += 10
		end := pos + size
		if end > len(packet) {
			return errors.New("truncated DNS record")
		}
		switch kind {
		case 1:
			if size != 4 {
				return errors.New("invalid A record")
			}
		case 28:
			if size != 16 {
				return errors.New("invalid AAAA record")
			}
		case 2, 5, 12, 39:
			nameEnd, e := skipName(packet, pos)
			if e != nil || nameEnd != end {
				return errors.New("invalid DNS name record")
			}
		case 6:
			next, e := skipName(packet, pos)
			if e != nil || next > end {
				return errors.New("invalid SOA record")
			}
			next, e = skipName(packet, next)
			if e != nil || next+20 != end {
				return errors.New("invalid SOA record")
			}
		}
		pos = end
	}
	if pos != len(packet) {
		return errors.New("unexpected trailing DNS data")
	}
	return nil
}
func IPv4Answers(query, response []byte) ([]net.IP, error) {
	if err := ValidateResponse(query, response); err != nil {
		return nil, err
	}
	pos, err := skipName(response, 12)
	if err != nil {
		return nil, err
	}
	pos += 4
	var addresses []net.IP
	for i := 0; i < int(binary.BigEndian.Uint16(response[6:8])); i++ {
		pos, err = skipName(response, pos)
		if err != nil || pos+10 > len(response) {
			return nil, errors.New("invalid DNS answer")
		}
		kind, class, size := binary.BigEndian.Uint16(response[pos:]), binary.BigEndian.Uint16(response[pos+2:]), int(binary.BigEndian.Uint16(response[pos+8:]))
		pos += 10
		if pos+size > len(response) {
			return nil, errors.New("invalid DNS answer data")
		}
		if kind == 1 && class == 1 && size == 4 {
			addresses = append(addresses, append(net.IP(nil), response[pos:pos+4]...))
		}
		pos += size
	}
	return addresses, nil
}
