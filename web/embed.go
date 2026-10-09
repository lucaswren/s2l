package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS 返回嵌入的前端静态资源（web/dist）
func FS() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
