package service

import "github.com/lucaswren/s2l/internal/model"

func (s *MappingService) SetManagementAccess(a model.ManagementAccess) error {
	return s.net.SetManagementAccess(a)
}
