import { test } from '@playwright/test'

// 复现用户报告：镜像源页长地址表格撑破容器。
// 用一条超长地址的 registry 验证页面无横向溢出。
test('镜像源长地址不溢出页面', async ({ page, request }) => {
  const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18080'
  await request.post(`${base}/api/registries`, { data: {
    name: 'overflow-check',
    address: 'https://a-very-long-instance-name-with-many-words.cn-beijing.cr.aliyuncs.com:8443/nested/path',
    type: 'anonymous',
  } })
  await page.goto('/#/registries')
  await page.waitForTimeout(800)
  const overflowed = await page.evaluate(() =>
    document.documentElement.scrollWidth > window.innerWidth + 1)
  if (overflowed) throw new Error(`页面横向溢出：scrollWidth=${await page.evaluate(() => document.documentElement.scrollWidth)} innerWidth=${await page.evaluate(() => window.innerWidth)}`)
})
