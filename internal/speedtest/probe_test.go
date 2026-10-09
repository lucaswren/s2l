package speedtest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func TestProbeInvalidAddr(t *testing.T) {
	res := Probe(context.Background(), model.SocksNode{
		ID:   "n1",
		Name: "bad",
		Addr: "not-a-host",
	}, DefaultBytes)
	if res.OK {
		t.Fatal("expected failure")
	}
	if res.Error == "" {
		t.Fatal("expected error message")
	}
}

func TestProbeDirections(t *testing.T) {
	for _, test := range []struct {
		name                 string
		downStatus, upStatus int
	}{
		{"both succeed", 200, 200},
		{"download fails but upload succeeds", 503, 200},
		{"upload fails but download succeeds", 200, 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			var uploaded atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/down":
					if r.Method != http.MethodGet || r.URL.Query().Get("bytes") != strconv.FormatInt(MinBytes, 10) {
						t.Error("unexpected download request")
					}
					w.WriteHeader(test.downStatus)
					if test.downStatus == 200 {
						_, _ = io.Copy(w, io.LimitReader(strings.NewReader(strings.Repeat("x", int(MinBytes))), MinBytes))
					}
				case "/up":
					if r.Method != http.MethodPost || r.ContentLength != MinBytes {
						t.Error("unexpected upload request")
					}
					n, err := io.Copy(io.Discard, r.Body)
					if err != nil {
						t.Error(err)
					}
					uploaded.Store(n)
					w.WriteHeader(test.upStatus)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			result := probeDirections(context.Background(), model.SocksNode{ID: "n1"}, 1, server.Client(), server.URL+"/down", server.URL+"/up")
			if result.Download.OK != (test.downStatus == 200) || result.Upload.OK != (test.upStatus == 200) {
				t.Fatalf("unexpected direction results: %+v", result)
			}
			if result.OK != (result.Download.OK && result.Upload.OK) || uploaded.Load() != MinBytes {
				t.Fatalf("upload missing or wrong overall status: %+v", result)
			}
			if result.Download.OK && (result.Download.SpeedMbps <= 0 || result.SpeedMbps != result.Download.SpeedMbps) {
				t.Fatal("download metrics or compatibility fields missing")
			}
			if result.Upload.OK && (result.Upload.Bytes != MinBytes || result.Upload.SpeedMbps <= 0) {
				t.Fatal("upload metrics missing")
			}
		})
	}
}

func TestDownloadRejectsTruncatedPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("short"))
	}))
	defer server.Close()
	result := download(context.Background(), server.Client(), server.URL, MinBytes)
	if result.OK || !strings.Contains(result.Error, "incomplete") {
		t.Fatalf("truncated download accepted: %+v", result)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestUploadRejectsUnsentOrFailedRequestBody(t *testing.T) {
	for _, send := range []bool{false, true} {
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if send {
				trace := httptrace.ContextClientTrace(r.Context())
				trace.WroteHeaders()
				_, _ = io.Copy(io.Discard, r.Body)
				trace.WroteRequest(httptrace.WroteRequestInfo{Err: errors.New("write interrupted")})
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		})}
		result := upload(context.Background(), client, "http://speed.test/up", MinBytes)
		if result.OK || result.Error == "" {
			t.Fatalf("unsent or interrupted upload accepted: %+v", result)
		}
	}
}

func TestTransferUnitsAndShortTransfer(t *testing.T) {
	result := finishTransfer(TransferResult{Bytes: 1_000_000}, 2*time.Second, 1_000_000)
	if !result.OK || result.SpeedMbps != 4 || result.SpeedBps != 500_000 || result.DurationMs != 2000 {
		t.Fatalf("incorrect speed units: %+v", result)
	}
	if finishTransfer(TransferResult{Bytes: 500}, time.Second, 1000).OK {
		t.Fatal("partial transfer should fail")
	}
}

func TestProbeTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	res := Probe(ctx, model.SocksNode{
		ID:   "n1",
		Name: "x",
		Addr: "127.0.0.1:1",
	}, MinBytes)
	if res.OK {
		t.Fatal("expected failure")
	}
}
