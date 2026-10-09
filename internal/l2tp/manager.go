package l2tp

import (
	"errors"
	"fmt"

	"github.com/lucaswren/s2l/internal/model"
)

// Manager 统一管理 chap-secrets + xl2tpd.conf + 服务重载
type Manager struct {
	Chap *ChapManager
	Conf *ConfManager
}

func NewManager(chapPath, confPath, optionsPath string) *Manager {
	return &Manager{
		Chap: NewChapManager(chapPath),
		Conf: NewConfManager(confPath, optionsPath, chapPath),
	}
}

// ApplyStart 写入账号并同步 xl2tpd 配置（active 为全部应对外暴露的运行中映射）
// 返回 (hardErr, reloadErr)：hardErr 必须失败；reloadErr 仅警告。
func (m *Manager) ApplyStart(mapping model.TunnelMapping, active []model.TunnelMapping) (error, error) {
	if err := m.Chap.Upsert(mapping); err != nil {
		return fmt.Errorf("chap-secrets: %w", err), nil
	}
	if err := m.Conf.Sync(active); err != nil {
		rollbackChapErr := m.Chap.Remove(mapping.ID)
		rollbackConfErr := m.Conf.Sync(activeWithout(active, mapping.ID))
		rollbackErr := errors.Join(rollbackChapErr, rollbackConfErr)
		return errors.Join(fmt.Errorf("xl2tpd.conf: %w", err), rollbackErr), nil
	}
	return nil, Reload()
}

// ApplyStop 移除账号并同步配置
func (m *Manager) ApplyStop(mappingID string, active []model.TunnelMapping) error {
	if err := m.Chap.Remove(mappingID); err != nil {
		return fmt.Errorf("chap-secrets: %w", err)
	}
	if err := m.Conf.Sync(active); err != nil {
		return fmt.Errorf("xl2tpd.conf: %w", err)
	}
	if err := Reload(); err != nil {
		return fmt.Errorf("reload xl2tpd: %w", err)
	}
	return nil
}

func activeWithout(active []model.TunnelMapping, id string) []model.TunnelMapping {
	out := make([]model.TunnelMapping, 0, len(active))
	for _, m := range active {
		if m.ID != id {
			out = append(out, m)
		}
	}
	return out
}

// SyncConfOnly 仅重写 xl2tpd.conf（开机恢复批量用）
func (m *Manager) SyncConfOnly(active []model.TunnelMapping) error {
	if err := m.Conf.Sync(active); err != nil {
		return err
	}
	return Reload()
}
