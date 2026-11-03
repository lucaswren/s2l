package api

import (
	"archive/zip"
	"bytes"
	"net/http"

	"github.com/lucaswren/s2l/internal/evidence"
)

func (s *Server) handleEvidence(w http.ResponseWriter, r *http.Request) {
	if s.svc == nil || s.svc.Evidence() == nil {
		writeError(w, 503, "故障取证不可用")
		return
	}
	recorder := s.svc.Evidence()
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		var body struct {
			Enabled   *bool  `json:"enabled"`
			MappingID string `json:"mapping_id"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if decodeJSON(r, &body) != nil || (body.Enabled == nil) == (body.MappingID == "") {
			writeError(w, 400, "请选择取证开关或一条映射")
			return
		}
		if body.Enabled != nil {
			if err := recorder.SetEnabled(*body.Enabled); err != nil {
				writeError(w, 500, "无法保存取证设置")
				return
			}
		} else if !s.svc.CaptureEvidence(body.MappingID) {
			writeError(w, 409, "无法开始取证：请检查映射是否运行、取证是否开启，或等待当前采集及 10 分钟冷却结束")
			return
		}
	default:
		w.WriteHeader(405)
		return
	}
	view := recorder.View()
	view.PacketCaptureAvailable = evidence.Available()
	writeJSON(w, 200, view)
}
func (s *Server) handleEvidenceExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	if s.svc == nil || s.svc.Evidence() == nil {
		writeError(w, 503, "故障取证不可用")
		return
	}
	select {
	case s.debugExports <- struct{}{}:
		defer func() { <-s.debugExports }()
	default:
		writeError(w, 429, "导出忙，请稍后重试")
		return
	}
	entries, err := s.svc.Evidence().Read(r.URL.Query().Get("id"))
	if err != nil {
		writeError(w, 404, "取证记录不存在或已轮换")
		return
	}
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	for name, b := range entries {
		file, err := zw.Create(name)
		if err != nil {
			writeError(w, 500, "无法创建取证包")
			return
		}
		if _, err = file.Write(b); err != nil {
			writeError(w, 500, "无法写入取证包")
			return
		}
	}
	if zw.Close() != nil {
		writeError(w, 500, "无法完成取证包")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="s2l-evidence.zip"`)
	_, _ = w.Write(buffer.Bytes())
}
