package api

import (
	"io/fs"
	"net/http"
	"strings"
)

// Static 提供嵌入的前端页面。带哈希的 assets 长期缓存；其余路径都返回 index.html（前端路由），且不缓存。
func Static(dist fs.FS) http.Handler {
	files := http.FileServerFS(dist)
	index, _ := fs.ReadFile(dist, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if strings.HasPrefix(path, "assets/") {
			if _, err := fs.Stat(dist, path); err == nil {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				files.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}
		if index == nil {
			http.Error(w, "前端还没有构建，请先运行 make build", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}
