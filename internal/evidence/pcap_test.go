package evidence

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func testPCAP(link uint32, proto byte, body []byte) []byte {
	var offset int
	switch link {
	case 1:
		offset = 14
	case 113:
		offset = 16
	case 276:
		offset = 20
	}
	packet := make([]byte, offset+20+len(body))
	switch link {
	case 1:
		binary.BigEndian.PutUint16(packet[12:14], 0x800)
	case 113:
		binary.BigEndian.PutUint16(packet[14:16], 0x800)
	case 276:
		binary.BigEndian.PutUint16(packet[:2], 0x800)
	}
	packet[offset] = 0x45
	packet[offset+9] = proto
	binary.BigEndian.PutUint16(packet[offset+2:offset+4], uint16(20+len(body)))
	copy(packet[offset+20:], body)
	h := make([]byte, 24)
	copy(h, []byte{0xd4, 0xc3, 0xb2, 0xa1})
	binary.LittleEndian.PutUint16(h[4:6], 2)
	binary.LittleEndian.PutUint16(h[6:8], 4)
	binary.LittleEndian.PutUint32(h[16:20], 160)
	binary.LittleEndian.PutUint32(h[20:24], link)
	record := make([]byte, 16)
	binary.LittleEndian.PutUint32(record[8:12], uint32(len(packet)))
	binary.LittleEndian.PutUint32(record[12:16], uint32(len(packet)))
	return append(append(h, record...), packet...)
}
func TestPacketHeadersExcludeCredentialsAndPayload(t *testing.T) {
	for _, link := range []uint32{1, 113, 276, 101} {
		for _, proto := range []byte{6, 17, 1} {
			header := 8
			if proto == 6 {
				header = 20
			}
			body := append(make([]byte, header), []byte("PAP password and SOCKS authentication secret")...)
			if proto == 6 {
				body[12] = 0x50
			}
			original := testPCAP(link, proto, body)
			got, count, err := HeaderPCAP(bytes.NewReader(original))
			if err != nil || count != 1 {
				t.Fatal(link, proto, count, err)
			}
			if bytes.Contains(got, []byte("password")) || bytes.Contains(got, []byte("secret")) {
				t.Fatal("payload leaked")
			}
			if binary.LittleEndian.Uint32(got[36:40]) != binary.LittleEndian.Uint32(original[36:40]) {
				t.Fatal("wire length changed")
			}
			if len(got) >= len(original) {
				t.Fatal("capture not truncated")
			}
		}
	}
}

func TestTCPOptionsPreserveTimestampsAndEraseCookies(t *testing.T) {
	body := make([]byte, 40)
	body[12] = 0xa0
	copy(body[20:], []byte{8, 10, 0, 0, 0, 1, 0, 0, 0, 2, 34, 10, 'p', 'a', 's', 's', 'w', 'o', 'r', 'd'})
	data := testPCAP(101, 6, append(body, []byte("application-secret")...))
	got, count, err := HeaderPCAP(bytes.NewReader(data))
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if bytes.Contains(got, []byte("password")) || bytes.Contains(got, []byte("application")) {
		t.Fatal("option cookie or payload retained")
	}
	if !bytes.Equal(got[80:90], body[20:30]) {
		t.Fatal("timestamp option lost")
	}
}
func TestPacketReaderRejectsUnsupportedAndMalformedStreams(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("bad!"), testPCAP(999, 17, make([]byte, 8))} {
		if _, _, err := HeaderPCAP(bytes.NewReader(data)); err == nil {
			t.Fatal("accepted invalid pcap")
		}
	}
	data := testPCAP(101, 17, append(make([]byte, 8), []byte("fragment-secret")...))
	data[40+6] = 0
	data[40+7] = 1
	got, count, err := HeaderPCAP(bytes.NewReader(data))
	if err != nil || count != 0 || len(got) != 24 {
		t.Fatal(count, err, "fragment retained")
	}
}

func TestMalformedIPLengthCannotTurnPaddingIntoHeaders(t *testing.T) {
	body := make([]byte, 40)
	body[12] = 0x50
	data := testPCAP(101, 6, body)
	binary.BigEndian.PutUint16(data[42:44], 20)
	got, count, err := HeaderPCAP(bytes.NewReader(data))
	if err != nil || count != 0 || len(got) != 24 {
		t.Fatal("captured transport outside declared IP length", count, err)
	}
}
