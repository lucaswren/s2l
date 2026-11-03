package speedtest

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strings"
)

const healthDNSName = "example.com"

func dnsQuery() ([]byte, error) {
	q := make([]byte, 12)
	if _, err := rand.Read(q[:2]); err != nil {
		return nil, err
	}
	q[2] = 1
	q[5] = 1 // recursion desired; exactly one question
	for _, label := range strings.Split(healthDNSName, ".") {
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	return append(q, 0, 0, 1, 0, 1), nil // A, IN
}

// Bounds and pointer-loop checks are required even for trusted public resolvers.
func dnsName(packet []byte, start int) (string, int, error) {
	pos, next := start, -1
	labels := []string{}
	seen := map[int]bool{}
	length := 0
	for steps := 0; steps < 128; steps++ {
		if pos >= len(packet) || seen[pos] {
			break
		}
		seen[pos] = true
		size := int(packet[pos])
		pos++
		if size == 0 {
			if next < 0 {
				next = pos
			}
			return strings.Join(labels, "."), next, nil
		}
		if size&0xc0 == 0xc0 {
			if pos >= len(packet) {
				break
			}
			offset := (size&0x3f)<<8 | int(packet[pos])
			pos++
			if next < 0 {
				next = pos
			}
			pos = offset
			continue
		}
		if size > 63 || pos+size > len(packet) {
			break
		}
		length += size + 1
		if length > 254 {
			break
		}
		labels = append(labels, string(packet[pos:pos+size]))
		pos += size
	}
	return "", 0, fmt.Errorf("invalid DNS name")
}

func validateDNS(query, response []byte) error {
	invalid := fmt.Errorf("invalid or unsuccessful DNS response")
	if len(query) < 12 || len(response) < 12 || binary.BigEndian.Uint16(response[:2]) != binary.BigEndian.Uint16(query[:2]) {
		return invalid
	}
	flags := binary.BigEndian.Uint16(response[2:4])
	if flags&0x8000 == 0 || flags&0x7800 != 0 || flags&0x0200 != 0 || flags&0xf != 0 || binary.BigEndian.Uint16(response[4:6]) != 1 {
		return invalid
	}
	name, next, err := dnsName(response, 12)
	if err != nil || !strings.EqualFold(name, healthDNSName) || next+4 > len(response) || binary.BigEndian.Uint16(response[next:next+2]) != 1 || binary.BigEndian.Uint16(response[next+2:next+4]) != 1 {
		return invalid
	}
	pos := next + 4
	answers := int(binary.BigEndian.Uint16(response[6:8]))
	if answers == 0 || answers > 256 {
		return invalid
	}
	resolved := false
	allowed := map[string]bool{healthDNSName: true}
	type answer struct {
		name        string
		kind, class uint16
		start, end  int
	}
	records := []answer{}
	for i := 0; i < answers; i++ {
		owner, after, err := dnsName(response, pos)
		if err != nil || after+10 > len(response) {
			return invalid
		}
		end := after + 10 + int(binary.BigEndian.Uint16(response[after+8:after+10]))
		if end > len(response) {
			return invalid
		}
		records = append(records, answer{strings.ToLower(owner), binary.BigEndian.Uint16(response[after : after+2]), binary.BigEndian.Uint16(response[after+2 : after+4]), after + 10, end})
		pos = end
	}
	// Follow CNAME chains regardless of answer ordering, with a bounded walk.
	for pass := 0; pass < len(records); pass++ {
		changed := false
		for _, r := range records {
			if r.class == 1 && r.kind == 5 && allowed[r.name] {
				alias, end, err := dnsName(response, r.start)
				if err != nil || end != r.end {
					return invalid
				}
				key := strings.ToLower(alias)
				if !allowed[key] {
					allowed[key] = true
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	for _, r := range records {
		if r.class == 1 && r.kind == 1 && r.end-r.start == 4 && allowed[r.name] {
			resolved = true
		}
	}
	if !resolved {
		return invalid
	}
	return nil
}
