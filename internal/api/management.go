package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/lucaswren/s2l/internal/config"
	"github.com/lucaswren/s2l/internal/model"
)

func parseListeningPorts(output string) map[int]bool {
	ports := map[int]bool{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] != "LISTEN" {
			continue
		}
		i := strings.LastIndex(fields[3], ":")
		if i < 0 {
			continue
		}
		p, e := strconv.Atoi(fields[3][i+1:])
		if e == nil && p > 0 && p <= 65535 {
			ports[p] = true
		}
	}
	return ports
}
func listeningPorts(ctx context.Context) (map[int]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ss", "-H", "-lnt").Output()
	return parseListeningPorts(string(out)), err
}

func (s *Server) handlePPPManagement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	if r.Method == http.MethodPost && !s.settingsWrite(w, r) {
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	data, err := os.ReadFile(s.configPath)
	if err != nil {
		writeError(w, 409, "无法读取受管配置文件")
		return
	}
	cfg, err := config.Load(s.configPath)
	if err != nil {
		writeError(w, 500, "配置文件格式错误")
		return
	}
	if r.Method == http.MethodGet {
		sources := []map[string]string{}
		for _, m := range s.store.ListMappings() {
			sources = append(sources, map[string]string{"id": m.ID, "subnet": m.Subnet, "name": m.L2TPUser})
		}
		ports, _ := sshPorts()
		_, webPort, _ := s.webPortSettings()
		if webPort > 0 {
			ports = append(ports, webPort)
		}
		listening, listenErr := listeningPorts(r.Context())
		active := []int{}
		for _, p := range ports {
			if listening[p] {
				active = append(active, p)
			}
		}
		local := []string{}
		if addresses, e := net.InterfaceAddrs(); e == nil {
			for _, address := range addresses {
				ip, _, e := net.ParseCIDR(address.String())
				if e == nil && ip.To4() != nil && !ip.IsLoopback() {
					local = append(local, ip.String())
				}
			}
		}
		writeJSON(w, 200, map[string]any{"access": cfg.PPPManagement, "sources": sources, "detected_ports": active, "configured_ports": ports, "port_check_available": listenErr == nil, "local_ipv4": local, "available": runtime.GOOS == "linux" && os.Geteuid() == 0 && s.applyManagement != nil})
		return
	}
	if s.applyManagement == nil {
		writeError(w, 409, "当前服务不支持修改 PPP 管理访问")
		return
	}
	var access model.ManagementAccess
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if decodeJSON(r, &access) != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	old := cfg.PPPManagement
	cfg.PPPManagement = access
	if err := cfg.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		writeError(w, 500, "配置文件格式错误")
		return
	}
	fields["ppp_management"], _ = json.Marshal(access)
	updated, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		writeError(w, 500, "无法生成配置")
		return
	}
	if err = s.applyManagement(access); err != nil {
		writeError(w, 409, "应用失败："+err.Error())
		return
	}
	if err = writeSettings(s.configPath, append(updated, '\n')); err != nil {
		if rollback := s.applyManagement(old); rollback != nil {
			writeError(w, 500, "保存失败且运行策略恢复失败，请通过原管理线路检查服务")
			return
		}
		writeError(w, 500, "保存失败，已恢复原管理例外")
		return
	}
	writeJSON(w, 200, map[string]any{"access": access, "message": "PPP 管理访问已更新；不改变固定出口和 UDP 策略"})
}
