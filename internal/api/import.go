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
	textImport := len(nodes) == 0 && strings.TrimSpace(req.Text) != ""
	var parseErrs []string
	if textImport {
		nodes, parseErrs = parseNodeText(req.Text)
	}
	res := importResult{Skipped: len(parseErrs), Errors: append([]string{}, parseErrs...)}
	out := make([]model.SocksNode, 0, len(nodes))
	existing := s.store.ListNodes()
	for i, n := range nodes {
		n.Name = strings.TrimSpace(n.Name)
		n.Addr = strings.TrimSpace(n.Addr)
		if err := validateSocksNode(n); err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		if n.Name == "" {
			n.Name = n.Addr
		}
		if !validGroup(n.Group) {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("row %d: invalid group", i+1))
			continue
		}
		isNew := n.ID == ""
		if isNew {
			matches := 0
			for _, e := range existing {
				// Shared proxy endpoints commonly use credentials to select the
				// exit. An address or display name alone is not a node identity.
				if e.Addr == n.Addr && e.Username == n.Username && e.Password == n.Password {
					matches++
					n.ID = e.ID
					isNew = false
					if textImport {
						n.Group = e.Group
						if n.Name == n.Addr {
							n.Name = e.Name
						}
					}
				}
			}
			if matches > 1 {
				res.Skipped++
				res.Errors = append(res.Errors, fmt.Sprintf("row %d: multiple identical nodes; specify a node id", i+1))
				continue
			}
		} else {
			_, exists := s.store.GetNode(n.ID)
			isNew = !exists
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
	textImport := len(maps) == 0 && strings.TrimSpace(req.Text) != ""
	parseErrs := []string{}
	if textImport {
		maps, parseErrs = parseMappingText(req.Text, s)
	}
	res := importResult{Skipped: len(parseErrs), Errors: append([]string{}, parseErrs...)}
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
		if !validGroup(m.Group) {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("row %d: invalid group", i+1))
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
			if existing.Status != model.StatusStopped {
				res.Skipped++
				res.Errors = append(res.Errors, fmt.Sprintf("row %d: mapping %s is not stopped, clean up and stop first", i+1, m.ID))
				continue
			}
			// The documented text format has no network tuning columns.
			// Updating credentials through it must preserve existing tuning.
			if textImport {
				m.Group = existing.Group
				m.MTU, m.MSS, m.QueueLength = existing.MTU, existing.MSS, existing.QueueLength
				m.ObserveConnections = existing.ObserveConnections
				m.UploadKbps, m.DownloadKbps, m.MaxConnections, m.MaxSessions = existing.UploadKbps, existing.DownloadKbps, existing.MaxConnections, existing.MaxSessions
				m.UDPPolicy, m.DNSMode = existing.UDPPolicy, existing.DNSMode
				m.DNSDoHURL, m.DNSTCPAddress = existing.DNSDoHURL, existing.DNSTCPAddress
			}
			m.Status = existing.Status
		} else {
			isNew = true
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

// parseNodeText accepts the documented SOCKS5 import formats, one node per line:
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
	if low == "host:port:user:pass" || low == "ip:port:user:pass" || low == "host:port" {
		return true
	}
	fields := splitFields(low)
	if len(fields) != 4 {
		return false
	}
	return ((fields[0] == "name" && (fields[1] == "addr" || fields[1] == "host:port")) ||
		((fields[0] == "host" || fields[0] == "ip") && fields[1] == "port")) &&
		(fields[2] == "user" || fields[2] == "username") && (fields[3] == "pass" || fields[3] == "password")
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
	if strings.ContainsAny(line, ",|\t") {
		return parseNodeDelimited(line)
	}
	if strings.Contains(line, "@") {
		// The colon form also allows @ in usernames and passwords.
		atNode, atErr := parseNodeAt(line)
		colonNode, colonErr := parseNodeColon(line)
		if atErr == nil && colonErr == nil {
			return model.SocksNode{}, fmt.Errorf("ambiguous @ form; use socks5://user:pass@host:port")
		}
		if atErr == nil {
			return atNode, nil
		}
		if colonErr == nil {
			return colonNode, nil
		}
		return model.SocksNode{}, atErr
	}
	return parseNodeColon(line)
}

func parseNodeURL(raw string) (model.SocksNode, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return model.SocksNode{}, fmt.Errorf("invalid url")
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return model.SocksNode{}, fmt.Errorf("SOCKS URL must not contain a path, query or fragment; percent-encode credentials")
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
	// Passwords may contain @ in either orientation. Numeric passwords can
	// make both orientations valid: refuse ambiguity instead of changing exits.
	var candidates []model.SocksNode
	for _, form := range []struct{ endpoint, credentials string }{
		{line[:strings.IndexByte(line, '@')], line[strings.IndexByte(line, '@')+1:]},
		{line[strings.LastIndexByte(line, '@')+1:], line[:strings.LastIndexByte(line, '@')]},
	} {
		host, port, err := net.SplitHostPort(form.endpoint)
		user, pass, ok := splitUserPass(form.credentials)
		if err == nil && host != "" && !strings.ContainsAny(host, "@ /\\") && looksLikePort(port) && ok {
			candidates = append(candidates, model.SocksNode{Name: form.endpoint, Addr: form.endpoint, Username: user, Password: pass})
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		return model.SocksNode{}, fmt.Errorf("ambiguous @ form; use socks5://user:pass@host:port")
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
	if len(parts) < 2 || len(parts) > 4 {
		return model.SocksNode{}, fmt.Errorf("need 2-4 fields; use a SOCKS URL for credentials containing separators")
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
			if len(parts) > 3 {
				return model.SocksNode{}, fmt.Errorf("unnamed node needs at most 3 fields; use a SOCKS URL for credentials containing separators")
			}
			n := model.SocksNode{Name: parts[0], Addr: parts[0]}
			if len(parts) > 1 {
				n.Username = parts[1]
			}
			if len(parts) > 2 {
				n.Password = parts[2]
			}
			return n, nil
		}
		return model.SocksNode{}, fmt.Errorf("invalid address field")
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
	if host, port, err := net.SplitHostPort(line); err == nil && host != "" && !strings.ContainsAny(host, "@ /\\") && looksLikePort(port) {
		return model.SocksNode{Name: line, Addr: line}, nil
	}
	// host:port:user:pass[:remark...]
	parts := strings.Split(line, ":")
	if len(parts) < 4 {
		return model.SocksNode{}, fmt.Errorf("want host:port:user:pass")
	}
	host, port := parts[0], parts[1]
	if host == "" || strings.ContainsAny(host, "@ /\\") || !looksLikePort(port) {
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
	addAlias := func(alias, id string) {
		if previous, exists := nodeByName[alias]; exists && previous != id {
			nodeByName[alias] = "" // Ambiguous; never pick a different fixed exit by list order.
		} else if !exists {
			nodeByName[alias] = id
		}
	}
	for _, n := range s.store.ListNodes() {
		addAlias(n.Name, n.ID)
		addAlias(n.Addr, n.ID)
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
		if len(parts) != 4 && len(parts) != 6 {
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
		if node, byID := s.store.GetNode(nodeRef); byID {
			nodeID, ok = node.ID, true
		}
		if !ok {
			errs = append(errs, fmt.Sprintf("line %d: unknown node %q", i+1, nodeRef))
			continue
		}
		if nodeID == "" {
			errs = append(errs, fmt.Sprintf("line %d: ambiguous node reference; use its node id", i+1))
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
