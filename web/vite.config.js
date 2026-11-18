import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { copyFileSync, mkdirSync } from 'node:fs'
import { resolve } from 'node:path'

// logo 单源约束：唯一源文件在 docs/logo.svg，构建期复制进产物（public/ 下的副本是生成物，不入库）。
function logoFromSingleSource() {
  return {
    name: 'logo-single-source',
    buildStart() {
      mkdirSync(resolve(__dirname, 'public'), { recursive: true })
      copyFileSync(resolve(__dirname, '../docs/logo.svg'), resolve(__dirname, 'public/logo.svg'))
    },
  }
}

// 构建产物输出到 web/dist，由仓库提交（纯 Go 环境可 make build），
// 经 internal/webui 的 go:embed 嵌入二进制。
export default defineConfig({
  plugins: [vue(), logoFromSingleSource()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8080',
    },
  },
})
