package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/lucaswren/s2l/internal/model"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"
)

func resourceRevision(v any) string {
	if m, ok := v.(model.TunnelMapping); ok {
		m.Status = ""
		m.ErrorMsg = ""
		v = m
	}
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func writeResource(w http.ResponseWriter, v any) {
	w.Header().Set("ETag", `"`+resourceRevision(v)+`"`)
	writeJSON(w, 200, v)
}
func revisionMatches(w http.ResponseWriter, r *http.Request, current any) bool {
	expected := r.Header.Get("If-Match")
	if expected != "" && expected != `"`+resourceRevision(current)+`"` {
		writeError(w, 409, "配置已被其他页面修改，请重新打开编辑后再保存")
		return false
	}
	return true
}
func (s *Server) previewChange(w http.ResponseWriter, r *http.Request, old, next any, nodeID string) bool {
	if r.URL.Query().Get("preview") != "1" {
		return false
	}
	a, b := map[string]any{}, map[string]any{}
	data, _ := json.Marshal(old)
	_ = json.Unmarshal(data, &a)
	data, _ = json.Marshal(next)
	_ = json.Unmarshal(data, &b)
	fields := []string{}
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	for k := range keys {
		if k == "id" || k == "status" || k == "error_msg" {
			continue
		}
		if !reflect.DeepEqual(a[k], b[k]) {
			fields = append(fields, k)
		}
	}
	sort.Strings(fields)
	ids := []string{}
	blocked := []map[string]string{}
	connectionChanged := false
	if node, ok := next.(model.SocksNode); ok {
		if previous, ok := old.(model.SocksNode); ok {
			connectionChanged = previous.Addr != node.Addr || previous.Username != node.Username || previous.Password != node.Password
		}
	}
	if mapping, ok := next.(model.TunnelMapping); ok {
		ids = append(ids, mapping.ID)
	} else {
		for _, m := range s.store.ListMappings() {
			if m.NodeID == nodeID {
				ids = append(ids, m.ID)
				if connectionChanged && m.Status != model.StatusStopped {
					blocked = append(blocked, map[string]string{"id": m.ID, "name": m.L2TPUser, "status": m.Status})
				}
			}
		}
	}
	message := "保存前已校验；运行中的映射需先停止。失败写入保留原配置，历史版本可在备份中恢复。"
	if _, ok := next.(model.SocksNode); ok {
		message = "保存不会重启映射。名称和分组可在线修改；连接参数需在关联映射停止后修改。"
	}
	writeJSON(w, 200, map[string]any{"fields": fields, "affected": ids, "blocked": blocked, "message": message})
	return true
}

// Restrict metadata to a single short label; it is never used in shell commands.
func validGroup(v string) bool {
	return utf8.RuneCountInString(v) <= 64 && !strings.ContainsAny(v, "\r\n\x00")
}
