package evidence

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// HeaderPCAP keeps IPv4 + TCP/UDP/ICMP headers, including whitelisted TCP
// options for retransmission/SACK/RTT analysis. UDP payload includes L2TP/PPP
// auth and is never persisted. Unknown TCP options are overwritten with NOPs.
// Unknown link types, IP options, noninitial fragments and non-IP fail closed.
func HeaderPCAP(src io.Reader) ([]byte, int, error) {
	h := make([]byte, 24)
	if _, err := io.ReadFull(src, h); err != nil {
		return nil, 0, err
	}
	var order binary.ByteOrder
	switch string(h[:4]) {
	case "\xd4\xc3\xb2\xa1", "\x4d\x3c\xb2\xa1":
		order = binary.LittleEndian
	case "\xa1\xb2\xc3\xd4", "\xa1\xb2\x3c\x4d":
		order = binary.BigEndian
	default:
		return nil, 0, fmt.Errorf("unsupported pcap format")
	}
	link := order.Uint32(h[20:24])
	if link != 1 && link != 113 && link != 276 && link != 101 {
		return nil, 0, fmt.Errorf("unsupported pcap link type %d", link)
	}
	var out bytes.Buffer
	out.Write(h)
	count := 0
	for i := 0; i < 4096; i++ {
		record := make([]byte, 16)
		_, err := io.ReadFull(src, record)
		if err != nil {
			if err == io.EOF {
				return out.Bytes(), count, nil
			}
			return out.Bytes(), count, err
		}
		n := order.Uint32(record[8:12])
		if n > 65536 {
			return out.Bytes(), count, fmt.Errorf("invalid packet size")
		}
		packet := make([]byte, n)
		if _, err := io.ReadFull(src, packet); err != nil {
			return out.Bytes(), count, err
		}
		offset := -1
		switch link {
		case 1:
			if len(packet) >= 14 && binary.BigEndian.Uint16(packet[12:14]) == 0x0800 {
				offset = 14
			}
		case 113:
			if len(packet) >= 16 && binary.BigEndian.Uint16(packet[14:16]) == 0x0800 {
				offset = 16
			}
		case 276:
			if len(packet) >= 20 && binary.BigEndian.Uint16(packet[:2]) == 0x0800 {
				offset = 20
			}
		case 101:
			offset = 0
		}
		if offset < 0 || len(packet) < offset+20 {
			continue
		}
		ip := packet[offset:]
		if ip[0] != 0x45 || binary.BigEndian.Uint16(ip[6:8])&0x1fff != 0 {
			continue
		}
		keep := 20
		switch ip[9] {
		case 6:
			if len(ip) < 40 {
				continue
			}
			size := int(ip[32]>>4) * 4
			if size < 20 || size > 60 || len(ip) < 20+size {
				continue
			}
			keep += size
			sanitizeTCPOptions(ip[40:keep])
		case 17:
			keep += 8
		case 1:
			keep += 8
		default:
			continue
		}
		if len(ip) < keep {
			continue
		}
		if int(binary.BigEndian.Uint16(ip[2:4])) < keep {
			continue
		}
		// Packet lengths remain the original wire lengths; truncated captures
		// are intentional and retain TCP sequence/ACK/flags/window for analysis.
		order.PutUint32(record[8:12], uint32(offset+keep))
		out.Write(record)
		out.Write(packet[:offset+keep])
		count++
	}
	return out.Bytes(), count, nil
}

func sanitizeTCPOptions(options []byte) {
	for i := 0; i < len(options); {
		kind := options[i]
		if kind == 0 {
			clear(options[i:])
			return
		}
		if kind == 1 {
			i++
			continue
		}
		if i+1 >= len(options) {
			clear(options[i:])
			return
		}
		n := int(options[i+1])
		if n < 2 || i+n > len(options) {
			clear(options[i:])
			return
		}
		known := (kind == 2 && n == 4) || (kind == 3 && n == 3) || (kind == 4 && n == 2) || (kind == 5 && n >= 10 && (n-2)%8 == 0) || (kind == 8 && n == 10)
		if !known {
			for j := i; j < i+n; j++ {
				options[j] = 1
			}
		}
		i += n
	}
}
