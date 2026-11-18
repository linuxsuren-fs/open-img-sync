import { test } from '@playwright/test'
import { mkdirSync } from 'node:fs'

// 截图生成（非 e2e 断言用例）：设置 SCREENSHOTS_DIR=../docs/screenshots 后运行，
//   在干净后端上造数据并截取各页面，用于 PR 描述与文档。
const dir = process.env.SCREENSHOTS_DIR
test.skip(!dir, '设置 SCREENSHOTS_DIR 后才生成截图')

async function postJSON(request, url, data) {
  const resp = await request.post(url, { data })
  return await resp.json()
}

test('生成页面截图', async ({ page, request }) => {
  mkdirSync(dir, { recursive: true })
  const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18080'

  // 造数据：服务器 ×2、镜像源 + 清单、一次同步任务（目标不可达，呈现失败态与日志）
  const srv1 = await postJSON(request, `${base}/api/servers`, {
    name: 'edge-prod-01', host: '10.0.1.11', auth_type: 'password',
    username: 'root', secret: 'demo-pass', tags: ['prod'],
  })
  const srv2 = await postJSON(request, `${base}/api/servers`, {
    name: 'edge-factory-02', host: '10.0.2.22', port: 2222, auth_type: 'private_key',
    username: 'deploy', secret: '-----BEGIN...', tags: ['factory'],
  })
  const reg = await postJSON(request, `${base}/api/registries`, {
    name: 'aliyun-ee-hangzhou', address: 'https://demo-registry.cn-hangzhou.cr.aliyuncs.com',
    type: 'aliyun-temp', role: 'source',
    aliyun: { region: 'cn-hangzhou', instance: 'demo-registry', access_key_id: 'LTAI5tXXXX', access_key_secret: 'sk' },
  })
  await postJSON(request, `${base}/api/registries`, {
    name: 'relay-internal', address: 'https://relay.internal:5000',
    type: 'standard', role: 'relay', username: 'sync', secret: 'relay-tok',
  })
  await request.post(`${base}/api/registries/${reg.id}/images/batch`, {
    data: { refs: 'library/nginx:1.27\nteam/app:v2.1\ninfra/agent@sha256:' + 'a'.repeat(64) },
  })
  const images = await (await request.get(`${base}/api/registries/${reg.id}/images`)).json()
  await postJSON(request, `${base}/api/tasks`, {
    mode: 'm1', image_ids: images.data.map(i => i.id), target_server_ids: [srv1.id, srv2.id],
  })
  await page.waitForTimeout(1500)

  await page.goto('/')
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.screenshot({ path: `${dir}/overview.png`, fullPage: true })

  await page.getByRole('button', { name: '服务器' }).click()
  await page.screenshot({ path: `${dir}/servers.png`, fullPage: true })

  await page.getByRole('button', { name: '镜像源' }).click()
  await page.screenshot({ path: `${dir}/registries.png`, fullPage: true })

  await page.getByRole('button', { name: '同步任务' }).click()
  await page.waitForTimeout(500)
  await page.screenshot({ path: `${dir}/tasks.png`, fullPage: true })
  await page.getByRole('button', { name: '详情' }).first().click()
  await page.waitForTimeout(2500)
  await page.screenshot({ path: `${dir}/task-detail.png`, fullPage: true })
  await page.getByRole('button', { name: '关闭' }).click()

  await page.getByRole('button', { name: '系统' }).click()
  await page.screenshot({ path: `${dir}/system.png`, fullPage: true })
})
