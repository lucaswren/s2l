package speedtest

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/lucaswren/s2l/internal/dnswire"
)

const healthDNSName = "example.com"

func dnsQuery() ([]byte, error) { return dnswire.QueryA(healthDNSName) }

func validateDNS(query, response []byte) error {
	invalid := fmt.Errorf("invalid or unsuccessful DNS response")
	if err := dnswire.ValidateResponse(query, response); err != nil {
		return err
	}
	if response[3]&15 != 0 {
		return invalid
	}
	name, next, err := dnswire.Name(response, 12)
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
		owner, after, err := dnswire.Name(response, pos)
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
				alias, end, err := dnswire.Name(response, r.start)
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
