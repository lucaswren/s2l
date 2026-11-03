package api

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/lucaswren/s2l/internal/speedtest"
)

type speedTestBody struct {
	Bytes int64 `json:"bytes"`
}

func (s *Server) handleNodeSpeedTest(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	node, ok := s.store.GetNode(id)
	if !ok {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	var body speedTestBody
	if r.Body != nil {
		defer r.Body.Close()
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := decodeJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), speedtest.DefaultTimeout)
	defer cancel()
	writeJSON(w, http.StatusOK, speedtest.Probe(ctx, node, body.Bytes))
}
