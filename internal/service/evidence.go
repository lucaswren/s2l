package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucaswren/s2l/internal/debuglog"
	"github.com/lucaswren/s2l/internal/evidence"
	"github.com/lucaswren/s2l/internal/model"
)

func (s *MappingService) Evidence() *evidence.Recorder { return s.evidence }
func (s *MappingService) AddEvidenceSecret(secret string) {
	if secret == "" {
		return
	}
	s.evidenceMu.Lock()
	defer s.evidenceMu.Unlock()
	s.evidenceSecrets = append(s.evidenceSecrets, secret)
}
func (s *MappingService) newEvidence() *evidence.Recorder {
	collect := func(ctx context.Context, req evidence.Request) evidence.Collected {
		if req.Node.ID == "" {
			req.Node, _ = s.store.GetNode(req.Mapping.NodeID)
		}
		result := evidence.CollectSystem(ctx, req)
		if s.singbox != nil {
			result.Snapshots["singbox-after"] = s.singbox.LogTail(req.Mapping.TunName)
		}
		for phase, tail := range map[string]string{"before": req.LogTail, "after": result.Snapshots["singbox-after"]} {
			if rejected := debuglog.ProxyRejections(tail); len(rejected) > 0 {
				sample, _ := json.Marshal(rejected)
				result.Snapshots["proxy-rejections-"+phase] = string(sample)
			}
		}
		events := s.operations.View().Events
		if len(events) > 80 {
			events = events[len(events)-80:]
		}
		b, _ := json.Marshal(events)
		result.Snapshots["operations"] = string(b)
		return result
	}
	return evidence.New(filepath.Join(s.store.DataDir(), "evidence"), collect, evidence.Available(), func(data []byte) []byte {
		s.evidenceMu.Lock()
		secrets := append([]string(nil), s.evidenceSecrets...)
		s.evidenceMu.Unlock()
		state := s.store.Snapshot()
		for _, m := range state.Mappings {
			secrets = append(secrets, m.L2TPPass)
		}
		for _, n := range state.Nodes {
			secrets = append(secrets, n.Password)
		}
		return evidence.CleanJSON(data, secrets)
	}, func(req evidence.Request) evidence.Request {
		req.Node, _ = s.store.GetNode(req.Mapping.NodeID)
		if current, ok := s.store.GetMapping(req.Mapping.ID); ok {
			req.Mapping.L2TPPass = current.L2TPPass
		}
		if s.singbox != nil {
			req.LogTail = s.singbox.LogTail(req.Mapping.TunName)
		}
		return req
	})
}
func (s *MappingService) recordFault(m model.TunnelMapping, reason string, details ...string) bool {
	if s.evidence == nil {
		return false
	}
	return s.evidence.Trigger(evidence.Request{Mapping: m, Reason: reason, Detail: strings.Join(details, "\n")}, time.Now())
}

// CaptureEvidence records a client-reported outage without repairing/restarting.
func (s *MappingService) CaptureEvidence(id string) bool {
	m, ok := s.store.GetMapping(id)
	return ok && m.Status == model.StatusRunning && s.recordFault(m, "manual")
}
func (s *MappingService) runEvidence(ctx context.Context) {
	if s.evidence != nil {
		s.evidence.Run(ctx)
	}
}
