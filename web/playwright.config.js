import { defineConfig } from '@playwright/test'

// e2e 前置：先启动后端（默认 http://127.0.0.1:18080，独立数据目录），
//   OIS_ADDR=127.0.0.1:18080 OIS_DATA_DIR=$(mktemp -d) open-img-sync serve
// 然后运行：npx playwright test
export default defineConfig({
  testDir: './e2e',
  timeout: 60000,
  use: {
    baseURL: process.env.E2E_BASE_URL || 'http://127.0.0.1:18080',
    screenshot: 'only-on-failure',
    locale: 'zh-CN',
    // 使用系统浏览器通道，避免 CI/弱网下载 Chromium；Windows 用 Edge，Linux 回退默认
    channel: process.env.PLAYWRIGHT_CHANNEL || undefined,
  },
  reporter: 'list',
})
