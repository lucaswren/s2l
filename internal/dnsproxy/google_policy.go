package dnsproxy

import (
	"encoding/binary"
	"strings"
)

// Google HTTPS discovery is optional: a NOERROR/empty answer leaves A/AAAA
// resolution available without advertising HTTP/3 on a TCP-only mapping.
// This cannot clear client Alt-Svc caches or control a private App resolver.
func googleHTTPSNoData(query []byte) ([]byte, bool) {
	q, err := question(query)
	if err != nil || query[2]&0xfe != 0 || query[3] != 0 || binary.BigEndian.Uint16(query[6:8]) != 0 || binary.BigEndian.Uint16(query[8:10]) != 0 || binary.BigEndian.Uint16(q[len(q)-4:]) != 65 || binary.BigEndian.Uint16(q[len(q)-2:]) != 1 {
		return nil, false
	}
	var labels []string
	for p := 12; query[p] != 0; {
		n := int(query[p])
		p++
		for _, ch := range query[p : p+n] {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
				return nil, false
			}
		}
		labels = append(labels, string(query[p:p+n]))
		p += n
	}
	host := strings.ToLower(strings.Join(labels, "."))
	matched := false
	for _, suffix := range []string{"google.com", "googleapis.com", "gstatic.com", "googlevideo.com", "youtube.com", "ytimg.com", "googleusercontent.com"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			matched = true
			break
		}
	}
	if !matched {
		return nil, false
	}
	end := 12 + len(q)
	additional := binary.BigEndian.Uint16(query[10:12])
	var opt []byte
	if additional == 0 {
		if end != len(query) {
			return nil, false
		}
	} else if additional == 1 {
		// Only a standard unsigned EDNS0 query is eligible. DNSSEC (DO, AD,
		// CD), TSIG, extended flags and malformed records pass upstream intact.
		if end+11 > len(query) || query[end] != 0 || binary.BigEndian.Uint16(query[end+1:]) != 41 || binary.BigEndian.Uint32(query[end+5:]) != 0 {
			return nil, false
		}
		n := int(binary.BigEndian.Uint16(query[end+9:]))
		if end+11+n != len(query) {
			return nil, false
		}
		// Validate option framing before returning a policy answer.
		for p := end + 11; p < len(query); {
			if p+4 > len(query) {
				return nil, false
			}
			size := int(binary.BigEndian.Uint16(query[p+2:]))
			p += 4 + size
			if p > len(query) {
				return nil, false
			}
		}
		opt = append([]byte(nil), query[end:end+11]...)
		opt[9], opt[10] = 0, 0
	} else {
		return nil, false
	}
	answer := make([]byte, end, len(query))
	copy(answer, query[:end])
	answer[2] = (query[2] & 1) | 0x80 // QR, original RD; never claim AA or AD.
	answer[3] = 0x80                  // RA, NOERROR; not NXDOMAIN or SERVFAIL.
	return append(answer, opt...), true
}
