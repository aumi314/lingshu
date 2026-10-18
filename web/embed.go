// Package web 把前端构建产物嵌进 Go 二进制。构建产物由 make build 生成（web/dist）。
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
