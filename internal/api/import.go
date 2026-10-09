package api

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
)

type importNodesReq struct {
	Text  string            `json:"text"`
	Nodes []model.SocksNode `json:"nodes"`
}

type importMappingsReq struct {
	Text     string                `json:"text"`
	Mappings []model.TunnelMapping `json:"mappings"`
}

type importResult struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
	Items   any      `json:"items,omitempty"`
}

func (s *Server) handleImportNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req importNodesReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	nodes := req.Nodes
	var parseErrs []string
	if len(nodes) == 0 && strings.TrimSpace(req.Text) != "" {
		nodes, parseErrs = parseNodeText(req.Text)
		if len(parseErrs) > 0 && len(nodes) == 0 {
			writeJSON(w, http.StatusBadRequest, importResult{Errors: parseErrs})
			return
		}
	}
	res := importResult{Errors: append([]string{}, parseErrs...)}
	out := make([]model.SocksNode, 0, len(nodes))
	existing := s.store.ListNodes()
	for i, n := range nodes {
		n.Name = strings.TrimSpace(n.Name)
		n.Addr = strings.TrimSpace(n.Addr)
		n.Username = strings.TrimSpace(n.Username)
		n.Password = strings.TrimSpace(n.Password)
		if err := validateSocksNode(n); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		if n.Name == "" {
			n.Name = n.Addr
		}
		isNew := n.ID == ""
		if isNew {
			for _, e := range existing {
				if e.Name == n.Name || e.Addr == n.Addr {
					n.ID = e.ID
					isNew = false
					break
				}
			}
		}
		if n.ID == "" {
			n.ID = newID("node_")
		}
		if err := s.store.UpsertNode(n); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		// 更新内存镜像，供同批次去重
		if isNew {
			existing = append(existing, n)
			res.Created++
		} else {
			for j := range existing {
				if existing[j].ID == n.ID {
					existing[j] = n
					break
				}
			}
			res.Updated++
		}
		out = append(out, n)
	}
	res.Items = redactNodes(out)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleImportMappings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req importMappingsReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	maps := req.Mappings
	parseErrs := []string{}
	if len(maps) == 0 && strings.TrimSpace(req.Text) != "" {
		maps, parseErrs = parseMappingText(req.Text, s)
	}
	res := importResult{Errors: append([]string{}, parseErrs...)}
	out := make([]model.TunnelMapping, 0, len(maps))
	for i, m := range maps {
		m.TunName = strings.TrimSpace(m.TunName)
		m.Subnet = strings.TrimSpace(m.Subnet)
		m.L2TPUser = strings.TrimSpace(m.L2TPUser)
		m.NodeID = strings.TrimSpace(m.NodeID)
		if err := validateMapping(m); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		if _, ok := s.store.GetNode(m.NodeID); !ok {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("row %d: node not found", i+1))
			continue
		}
		isNew := m.ID == ""
		if isNew {
			for _, existing := range s.store.ListMappings() {
				if existing.TableID == m.TableID || existing.TunName == m.TunName {
					m.ID = existing.ID
					isNew = false
					break
				}
			}
		}
		if err := s.checkMappingConflicts(m); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		if isNew {
			m.ID = newID("map_")
			m.Status = model.StatusStopped
		} else if existing, ok := s.store.GetMapping(m.ID); ok {
			if existing.Status == model.StatusRunning {
				res.Skipped++
				res.Errors = append(res.Errors, fmt.Sprintf("row %d: mapping %s is running, stop first", i+1, m.ID))
				continue
			}
			m.Status = existing.Status
		} else {
			m.Status = model.StatusStopped
		}
		if err := s.store.UpsertMapping(m); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		if isNew {
			res.Created++
		} else {
			res.Updated++
		}
		out = append(out, m)
	}
	res.Items = redactMappings(out)
	writeJSON(w, http.StatusOK, res)
}

// parseNodeText 兼容主流 SK5 导出格式（每行一条）：
//
//	host:port:user:pass
//	user:pass@host:port
//	host:port@user:pass
//	socks5://user:pass@host:port
//	host:port
//	name,host:port,user,pass   /  name|host:port|user|pass
//	host,port,user,pass
func parseNodeText(text string) ([]model.SocksNode, []string) {
	var out []model.SocksNode
	var errs []string
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if isNodeHeader(line) {
			continue
		}
		n, err := parseNodeLine(line)
		if err != nil {
			errs = append(errs, fmt.Sprintf("line %d: %v", i+1, err))
			continue
		}
		out = append(out, n)
	}
	return out, errs
}

func isNodeHeader(line string) bool {
	low := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.HasPrefix(low, "name,"), strings.HasPrefix(low, "name|"):
		return true
	case strings.HasPrefix(low, "host,"), strings.HasPrefix(low, "ip,"):
		return true
	case low == "host:port:user:pass", low == "ip:port:user:pass":
		return true
	case strings.HasPrefix(low, "host:port"):
		return !strings.ContainsAny(low[9:], "0123456789") // 纯说明行
	default:
		return false
	}
}

func parseNodeLine(line string) (model.SocksNode, error) {
	line = strings.TrimSpace(line)
	// 去掉行尾备注：空格+#
	if i := strings.Index(line, " #"); i > 0 {
		line = strings.TrimSpace(line[:i])
	}

	low := strings.ToLower(line)
	if strings.HasPrefix(low, "socks5://") || strings.HasPrefix(low, "socks://") {
		return parseNodeURL(line)
	}
	if strings.Contains(line, "@") {
		return parseNodeAt(line)
	}
	if strings.ContainsAny(line, ",|\t") {
		return parseNodeDelimited(line)
	}
	return parseNodeColon(line)
}

func parseNodeURL(raw string) (model.SocksNode, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return model.SocksNode{}, fmt.Errorf("invalid url")
	}
	host := u.Host
	if host == "" {
		return model.SocksNode{}, fmt.Errorf("url missing host")
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		// 无端口
		return model.SocksNode{}, fmt.Errorf("url need host:port")
	}
	n := model.SocksNode{Name: host, Addr: host}
	if u.User != nil {
		n.Username = u.User.Username()
		n.Password, _ = u.User.Password()
	}
	return n, nil
}

func parseNodeAt(line string) (model.SocksNode, error) {
	// user:pass@host:port  或  host:port@user:pass
	at := strings.LastIndex(line, "@")
	if at <= 0 || at >= len(line)-1 {
		return model.SocksNode{}, fmt.Errorf("invalid user@host form")
	}
	left, right := line[:at], line[at+1:]
	// 优先判定左侧为 host:port（避免把 user:pass 误当成 host:port）
	if _, port, err := net.SplitHostPort(left); err == nil && looksLikePort(port) {
		user, pass, ok := splitUserPass(right)
		if !ok {
			return model.SocksNode{}, fmt.Errorf("invalid host:port@user:pass")
		}
		return model.SocksNode{Name: left, Addr: left, Username: user, Password: pass}, nil
	}
	if _, port, err := net.SplitHostPort(right); err == nil && looksLikePort(port) {
		user, pass, ok := splitUserPass(left)
		if !ok {
			return model.SocksNode{}, fmt.Errorf("invalid user:pass@host:port")
		}
		return model.SocksNode{Name: right, Addr: right, Username: user, Password: pass}, nil
	}
	return model.SocksNode{}, fmt.Errorf("invalid @ form")
}

func splitUserPass(s string) (user, pass string, ok bool) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

func parseNodeDelimited(line string) (model.SocksNode, error) {
	parts := splitFields(line)
	if len(parts) < 2 {
		return model.SocksNode{}, fmt.Errorf("need host:port or name,addr")
	}

	// host,port,user,pass
	if len(parts) >= 4 && looksLikePort(parts[1]) && !strings.Contains(parts[0], ":") {
		addr := net.JoinHostPort(parts[0], parts[1])
		return model.SocksNode{
			Name:     addr,
			Addr:     addr,
			Username: parts[2],
			Password: parts[3],
		}, nil
	}

	// name,host:port[,user,pass]
	addr := parts[1]
	if _, _, err := net.SplitHostPort(addr); err != nil {
		// 也可能是 host:port,user,pass（无名称）
		if _, _, err2 := net.SplitHostPort(parts[0]); err2 == nil {
			n := model.SocksNode{Name: parts[0], Addr: parts[0]}
			if len(parts) > 1 {
				n.Username = parts[1]
			}
			if len(parts) > 2 {
				n.Password = parts[2]
			}
			return n, nil
		}
		return model.SocksNode{}, fmt.Errorf("invalid addr %q", addr)
	}
	n := model.SocksNode{Name: parts[0], Addr: addr}
	if len(parts) > 2 {
		n.Username = parts[2]
	}
	if len(parts) > 3 {
		n.Password = parts[3]
	}
	return n, nil
}

func parseNodeColon(line string) (model.SocksNode, error) {
	// host:port
	if _, _, err := net.SplitHostPort(line); err == nil {
		return model.SocksNode{Name: line, Addr: line}, nil
	}
	// host:port:user:pass[:remark...]
	parts := strings.Split(line, ":")
	if len(parts) < 4 {
		return model.SocksNode{}, fmt.Errorf("want host:port:user:pass")
	}
	host, port := parts[0], parts[1]
	if host == "" || !looksLikePort(port) {
		return model.SocksNode{}, fmt.Errorf("want host:port:user:pass")
	}
	user := parts[2]
	pass := strings.Join(parts[3:], ":") // 密码可含冒号；多余段并入密码
	// 若第 5 段起像备注（无特殊字符的短标签），可截断——保持并入密码更安全
	addr := net.JoinHostPort(host, port)
	return model.SocksNode{Name: addr, Addr: addr, Username: user, Password: pass}, nil
}

func looksLikePort(s string) bool {
	p, err := strconv.Atoi(s)
	return err == nil && p > 0 && p <= 65535
}

// parseMappingText 支持简写：
//
//	table_id,node_name_or_id,l2tp_user,l2tp_pass
//
// 或完整：
//
//	table_id,node_name_or_id,tun_name,subnet,l2tp_user,l2tp_pass
func parseMappingText(text string, s *Server) ([]model.TunnelMapping, []string) {
	var out []model.TunnelMapping
	var errs []string
	nodeByName := map[string]string{}
	for _, n := range s.store.ListNodes() {
		nodeByName[n.Name] = n.ID
		nodeByName[n.ID] = n.ID
		nodeByName[n.Addr] = n.ID
	}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		low := strings.ToLower(line)
		if strings.HasPrefix(low, "table") {
			continue
		}
		parts := splitFields(line)
		if len(parts) < 4 {
			errs = append(errs, fmt.Sprintf("line %d: need table_id,node,l2tp_user,l2tp_pass", i+1))
			continue
		}
		tableID, err := strconv.Atoi(parts[0])
		if err != nil || tableID <= 0 {
			errs = append(errs, fmt.Sprintf("line %d: invalid table_id", i+1))
			continue
		}
		nodeRef := parts[1]
		nodeID, ok := nodeByName[nodeRef]
		if !ok {
			errs = append(errs, fmt.Sprintf("line %d: unknown node %q", i+1, nodeRef))
			continue
		}
		m := model.TunnelMapping{
			NodeID:  nodeID,
			TableID: tableID,
			TunName: fmt.Sprintf("tun%d", tableID),
			Subnet:  fmt.Sprintf("10.0.%d.0/24", tableID%256),
		}
		if len(parts) >= 6 {
			m.TunName = parts[2]
			m.Subnet = parts[3]
			m.L2TPUser = parts[4]
			m.L2TPPass = parts[5]
		} else {
			m.L2TPUser = parts[2]
			m.L2TPPass = parts[3]
		}
		out = append(out, m)
	}
	return out, errs
}

func splitFields(line string) []string {
	sep := ","
	if strings.Contains(line, "\t") && strings.Count(line, "\t") >= strings.Count(line, ",") &&
		strings.Count(line, "\t") >= strings.Count(line, "|") {
		sep = "\t"
	} else if strings.Count(line, "|") >= strings.Count(line, ",") {
		sep = "|"
	}
	raw := strings.Split(line, sep)
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}
