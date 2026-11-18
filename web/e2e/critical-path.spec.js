import { test, expect } from '@playwright/test'

// 关键路径 e2e（#6 验收）：建服务器 → 建镜像清单 → 发起同步 → 看进度/日志。
// 目标地址与源 registry 都指向必然失败的地址，验证的是完整 UI 链路与
// 稳定错误码呈现（不依赖外部容器，CI 友好）。
test('关键路径：服务器 → 镜像清单 → 同步任务 → 步骤与日志', async ({ page }) => {
  await page.goto('/')

  // ---- 建服务器 ----
  await page.getByRole('button', { name: '服务器' }).click()
  await page.getByRole('button', { name: '新建服务器' }).click()
  await page.getByLabel('名称').fill('edge-e2e')
  await page.getByLabel('主机地址').fill('127.0.0.1')
  await page.getByLabel('用户名').fill('root')
  await page.getByPlaceholder('新建时必须填写凭据').or(page.locator('input[type=password]')).first().fill('e2e-password')
  await page.getByRole('button', { name: '保存' }).click()
  await expect(page.getByText('服务器已创建')).toBeVisible()
  // 凭据只回显掩码
  await expect(page.getByText('ssh-****').first()).toBeVisible()

  // ---- 建镜像源 + 批量录入 ----
  await page.getByRole('button', { name: '镜像源' }).click()
  await page.getByRole('button', { name: '新建镜像源' }).click()
  await page.getByLabel('名称').fill('src-e2e')
  await page.getByLabel('地址').fill('http://127.0.0.1:1')
  await page.getByRole('button', { name: '保存' }).click()
  await expect(page.getByText('镜像源已创建')).toBeVisible()

  await page.getByRole('row', { name: 'src-e2e' }).getByRole('button', { name: '镜像清单' }).click()
  await page.getByRole('button', { name: '批量录入' }).click()
  await page.getByPlaceholder(/每行一个镜像引用/).fill('nginx\nBAD:REF')
  await page.getByRole('button', { name: '录入' }).click()
  await expect(page.getByText('nginx:latest 已录入')).toBeVisible()
  await expect(page.getByText(/第 2 行/)).toBeVisible()
  await expect(page.getByText('nginx:latest', { exact: true })).toBeVisible()

  // ---- 发起同步 ----
  await page.getByRole('button', { name: '同步任务' }).click()
  await page.getByRole('checkbox').first().check() // 镜像
  await page.getByRole('checkbox').nth(1).check() // 服务器（同一样式）
  await page.getByRole('button', { name: '开始同步' }).click()
  await expect(page.getByText(/任务已创建/)).toBeVisible()

  // ---- 任务详情：步骤级状态与日志 ----
  await expect(page.locator('.dialog').first()).toBeVisible({ timeout: 20000 })
  // 源不可达 → 步骤失败，错误码映射为人话
  await expect(page.locator('.dialog table .tag--failed').first()).toBeVisible({ timeout: 30000 })
  await expect(page.getByText('registry 网络不可达').first()).toBeVisible()
  await expect(page.locator('.log-view')).toContainText('任务开始')
})

test('总览与系统页可见', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByText('最近任务')).toBeVisible()
  await page.getByRole('button', { name: '系统' }).click()
  await expect(page.getByText('版本信息')).toBeVisible()
  await expect(page.getByText('主密钥来源')).toBeVisible()
})

test('切换页签后刷新保持在当前页', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: '同步任务' }).click()
  await expect(page.locator('.shell__tab--active')).toHaveText('同步任务')
  await page.reload()
  // hash 记住了所在页：刷新后仍在同步任务页
  await expect(page.locator('.shell__tab--active')).toHaveText('同步任务')
  await expect(page.getByText('发起同步')).toBeVisible()
  // 浏览器返回：hash 回退到上一页
  await page.goBack()
  await expect(page.locator('.shell__tab--active')).toHaveText('总览')
})
