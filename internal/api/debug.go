package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"github.com/lucaswren/s2l/internal/debuglog"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"time"
)

func (s *Server) handleDebug(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decodeJSON(r, &body); err != nil || body.Enabled == nil {
			writeError(w, 400, "enabled must be a boolean")
			return
		}
		state := debuglog.Default.Set(*body.Enabled)
		if state.Enabled && s.svc != nil {
			sampledAt := time.Now()
			debuglog.Default.RecordSession(state.Session, s.svc.Status(), debuglog.SampleResources(s.store.DataDir()), sampledAt)
		}
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, 200, debuglog.Default.State())
}

func (s *Server) handleDebugAnalysis(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	writeJSON(w, 200, debuglog.Default.Analysis())
}

func (s *Server) handleDebugExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	select {
	case s.debugExports <- struct{}{}:
		defer func() { <-s.debugExports }()
	default:
		writeError(w, 429, "log export is busy; retry later")
		return
	}
	// Replace known credentials in all bundle entries, including error strings.
	secrets := []string{}
	s.authMu.RLock()
	secrets = append(secrets, s.adminPass)
	s.authMu.RUnlock()
	state := s.store.Snapshot()
	for _, n := range state.Nodes {
		secrets = append(secrets, n.Password)
	}
	for _, m := range state.Mappings {
		secrets = append(secrets, m.L2TPPass)
	}
	clean := secretCleaner(secrets)
	capture := debuglog.Default.Capture()
	manifest := map[string]any{"exported_at": time.Now().UTC(), "runtime": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "session": capture.State, "notice": "Logs and timeline share one captured session; status.json is current live status. Contains network addresses and destinations. Review before sharing. Existing tunnels retain their log level until restarted."}
	entries := map[string][]byte{"logs.txt": []byte(clean(string(capture.Logs)))}
	analysis := capture.Analysis
	entries["timeline.json"] = redactJSON(analysis.JSON(), clean)
	entries["analysis.txt"] = []byte(clean(string(analysis.Text())))
	entries["manifest.json"], _ = json.MarshalIndent(manifest, "", "  ")
	if s.svc != nil {
		status := s.svc.Status()
		status.Mappings = redactMappings(status.Mappings)
		b, _ := json.MarshalIndent(status, "", "  ")
		entries["status.json"] = redactJSON(b, clean)
	}
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	for name, b := range entries {
		f, err := zw.Create(name)
		if err != nil {
			writeError(w, 500, "cannot create archive")
			return
		}
		if _, err = f.Write(b); err != nil {
			writeError(w, 500, "cannot write archive")
			return
		}
	}
	if err := zw.Close(); err != nil {
		writeError(w, 500, "cannot finish archive")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="s2l-debug.zip"`)
	w.Write(buffer.Bytes())
}

// Replace longest matches in one pass. Replacing substrings sequentially can
// leave the suffix of an overlapping password visible in exported text.
func secretCleaner(secrets []string) func(string) string {
	// Health entries in logs.txt are JSON lines. Cover JSON/HTML-escaped
	// representations too, not only the literal secret in plain process output.
	variants := make([]string, 0, len(secrets)*3)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		variants = append(variants, secret)
		encoded, _ := json.Marshal(secret)
		escaped := string(encoded[1 : len(encoded)-1])
		variants = append(variants, escaped, strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&").Replace(escaped))
	}
	secrets = variants
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	pairs := []string{}
	seen := map[string]bool{}
	for _, s := range secrets {
		if s != "" && !seen[s] {
			pairs = append(pairs, s, "[REDACTED]")
			seen[s] = true
		}
	}
	replacer := strings.NewReplacer(pairs...)
	return replacer.Replace
}

// Sanitize decoded string values, then encode them again. A numeric password
// must not replace JSON numbers; quotes, HTML escapes and newlines must remain
// valid JSON rather than breaking exported timeline/status documents.
func redactJSON(data []byte, clean func(string) string) []byte {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var v any
	if err := decoder.Decode(&v); err != nil {
		return []byte(`{"error":"invalid export data"}`)
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			return clean(x)
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		case map[string]any:
			for k, val := range x {
				x[k] = walk(val)
			}
		}
		return v
	}
	out, _ := json.MarshalIndent(walk(v), "", "  ")
	return out
}
