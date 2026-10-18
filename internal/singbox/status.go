package singbox

import "github.com/lucaswren/s2l/internal/model"

type RuntimeStats struct {
	PID      int
	Resolver model.ResolverStatus
	Process  model.ProcessStatus
}
