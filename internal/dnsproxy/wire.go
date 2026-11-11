package dnsproxy

import (
	"encoding/binary"
	"errors"
	"net"

	"github.com/lucaswren/s2l/internal/dnswire"
)

func QueryA(host string) ([]byte, error) { return dnswire.QueryA(host) }

func ValidateResponse(query, response []byte) error {
	return dnswire.ValidateResponse(query, response)
}

func IPv4Answers(query, response []byte) ([]net.IP, error) {
	if err := ValidateResponse(query, response); err != nil {
		return nil, err
	}
	pos, err := dnswire.SkipName(response, 12)
	if err != nil {
		return nil, err
	}
	pos += 4
	var addresses []net.IP
	for i := 0; i < int(binary.BigEndian.Uint16(response[6:8])); i++ {
		pos, err = dnswire.SkipName(response, pos)
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
