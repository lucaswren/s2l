package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMTRArgumentsUseRawTCPAndNodePort(t *testing.T) {
	args := strings.Join(mtrArguments("203.0.113.1", "1080", "", true), " ")
	for _, part := range []string{"--report-cycles 8", "--raw", "--tcp --port 1080", "-4", "--interval 0.2", "--gracetime 2"} {
		if !strings.Contains(args, part) {
			t.Fatal(args)
		}
	}
	if strings.Contains(args, "--report ") {
		t.Fatal("raw trace still waits for report", args)
	}
	args = strings.Join(mtrArguments("2001:db8::1", "443", "icmp", false), " ")
	if !strings.Contains(args, "-6") || !strings.Contains(args, "--report-wide") || strings.Contains(args, "--tcp") {
		t.Fatal(args)
	}
}
func TestMTRStreamFlushesEarlyAndCapsOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	response := httptest.NewRecorder()
	stream := &networkStream{w: response, cancel: cancel}
	stream.send("start", map[string]any{"kind": "mtr"})
	if !response.Flushed {
		t.Fatal("start was not flushed")
	}
	output := &boundedOutput{}
	writer := &liveProbeOutput{capture: output, stream: stream}
	if n, err := writer.Write([]byte("h 0 203.0.113.1\np 0 1234 1\n")); err != nil || n == 0 {
		t.Fatal(n, err)
	}
	if !strings.Contains(response.Body.String(), `"type":"output"`) {
		t.Fatal("missing live output")
	}
	writer.Write([]byte(strings.Repeat("x", 50000)))
	before := response.Body.Len()
	writer.Write([]byte("more"))
	if response.Body.Len() != before || len(output.data) != 32768 {
		t.Fatal("unbounded stream")
	}
	stream.send("result", map[string]any{"ok": true})
	decoder := json.NewDecoder(response.Body)
	for decoder.More() {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
	}
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
}
func TestMTRReachedRequiresMatchingAddress(t *testing.T) {
	if !mtrReached("h 0 2001:db8::1\np 0 5 1\n", "2001:0db8::1") {
		t.Fatal("IPv6 target not recognized")
	}
	for _, output := range []string{"p 0 1000 1\n", "h 0 203.0.113.2\n", "mtr: permission denied\n"} {
		if mtrReached(output, "203.0.113.1") {
			t.Fatal(fmt.Sprintf("false reachability: %q", output))
		}
	}
}

func TestMTRLiveOutputPreservesSplitUTF8(t *testing.T) {
	response := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &boundedOutput{}
	stream := &networkStream{w: response, cancel: cancel}
	writer := &liveProbeOutput{capture: output, stream: stream}
	text := []byte("服务器返回错误\n")
	for _, b := range text {
		if _, err := writer.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	var received strings.Builder
	decoder := json.NewDecoder(response.Body)
	for decoder.More() {
		var event struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		received.WriteString(event.Data)
	}
	if received.String() != string(text) || string(output.data) != string(text) || ctx.Err() != nil {
		t.Fatal("UTF-8 stream corrupted", received.String())
	}
}
