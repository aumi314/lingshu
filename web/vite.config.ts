import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 开发时前端跑在 5173，接口和网关转给本地的灵枢（make dev，端口 8300）
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8300',
      '/v1': 'http://127.0.0.1:8300',
    },
  },
  // Ant Design 整体打包约 1.3MB（gzip 后 ~420KB），内部工具一次加载后走缓存，不做拆包
  build: { outDir: 'dist', emptyOutDir: true, chunkSizeWarningLimit: 1600 },
})
