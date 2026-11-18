<script setup>
import { ref, onMounted, onUnmounted, computed, watch } from 'vue'
import { createTask, listTasks, getTask, cancelTask, getTaskLogs, listServers, listRegistries, listImages, errorText } from '../api.js'

const props = defineProps({ active: Boolean })

const rows = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const statusFilter = ref('')
const toast = ref('')

// 创建表单
const mode = ref('m1')
const images = ref([])
const servers = ref([])
const relays = ref([])
const registryImages = ref({})
const selectedImages = ref([])
const selectedServers = ref([])
const selectedRelay = ref('')
const creating = ref(false)
const formErr = ref('')

// 详情
const detail = ref(null)
const logs = ref([])
let pollTimer = null
let logTimer = null
let lastSeq = 0

async function load() {
  try {
    const res = await listTasks({ page: page.value, page_size: pageSize, status: statusFilter.value || undefined })
    rows.value = Array.isArray(res.data) ? res.data : []
    total.value = res.total || 0
  } catch (e) {
    toast.value = e.message
  }
}
async function loadFormOptions() {
  try {
    const [srvRes, regRes] = await Promise.all([listServers({ page_size: 100 }), listRegistries({ page_size: 100 })])
    servers.value = srvRes.data || []
    const regs = regRes.data || []
    relays.value = regs.filter(r => r.role === 'relay')
    for (const reg of regs.filter(r => r.role === 'source')) {
      try {
        const imgRes = await listImages(reg.id, 1)
        registryImages.value[reg.id] = { name: reg.name, items: imgRes.data || [] }
        // 首页只取 20 条，够首发形态；更多清单在镜像源页维护
      } catch { /* 单个来源失败不影响整体 */ }
    }
    images.value = Object.entries(registryImages.value)
  } catch { /* 表单加载失败在提交时提示 */ }
}
onMounted(() => {
  load()
  loadFormOptions()
})
// 标签页为 v-show 常驻挂载：每次切入时刷新表单选项与列表
watch(() => props.active, (v) => {
  if (!v) return
  load()
  loadFormOptions()
})
onUnmounted(() => { stopDetailPolling() })

async function submit() {
  formErr.value = ''
  if (!selectedImages.value.length) {
    formErr.value = '请选择要同步的镜像'
    return
  }
  if (mode.value === 'm1' && !selectedServers.value.length) {
    formErr.value = 'SSH 直推模式需要选择目标服务器'
    return
  }
  if (mode.value === 'm2' && !selectedRelay.value) {
    formErr.value = '中转模式需要选择中转 registry'
    return
  }
  creating.value = true
  try {
    const payload = { mode: mode.value, image_ids: selectedImages.value }
    if (mode.value === 'm1') payload.target_server_ids = selectedServers.value
    else payload.relay_registry_id = selectedRelay.value
    const task = await createTask(payload)
    toast.value = `任务已创建：${task.id}`
    selectedImages.value = []
    selectedServers.value = []
    selectedRelay.value = ''
    await load()
    openDetail(task.id)
  } catch (e) {
    formErr.value = errorText(e.code, e.message)
  } finally {
    creating.value = false
  }
}

function openDetail(tid) {
  detail.value = null
  logs.value = []
  lastSeq = 0
  stopDetailPolling()
  pollTimer = setInterval(async () => {
    try {
      const t = await getTask(tid)
      detail.value = t
      if (isTerminal(t.status)) stopDetailPolling()
    } catch { /* 短暂失败忽略 */ }
  }, 1000)
  logTimer = setInterval(async () => {
    try {
      const res = await getTaskLogs(tid, lastSeq)
      const lines = res.data || []
      if (lines.length) {
        logs.value.push(...lines)
        lastSeq = lines[lines.length - 1].seq
      }
    } catch { /* 短暂失败忽略 */ }
  }, 1000)
  getTask(tid).then(t => { detail.value = t }).catch(() => {})
}
function stopDetailPolling() {
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
  if (logTimer) { clearInterval(logTimer); logTimer = null }
}
function closeDetail() {
  stopDetailPolling()
  detail.value = null
}
async function handleCancel(tid) {
  if (!window.confirm('确定取消该任务？')) return
  try {
    await cancelTask(tid)
    toast.value = '已发送取消请求'
  } catch (e) {
    toast.value = errorText(e.code, e.message)
  }
}
function isTerminal(status) {
  return ['success', 'failed', 'canceled'].includes(status)
}
function statusTag(status) {
  return { success: 'tag--success', failed: 'tag--failed', running: 'tag--running', canceled: 'tag--canceled' }[status] || 'tag--pending'
}
function statusText(status) {
  return { queued: '排队中', running: '运行中', success: '成功', failed: '失败', canceled: '已取消', pending: '等待', skipped: '已存在' }[status] || status
}
function fmtBytes(n) {
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`
}
const stepProgress = computed(() => {
  if (!detail.value || !detail.value.steps) return 0
  const steps = detail.value.steps
  const done = steps.filter(s => ['success', 'skipped', 'failed', 'canceled'].includes(s.status)).length
  return Math.round((done / steps.length) * 100)
})
function totalPages() { return Math.max(1, Math.ceil(total.value / pageSize)) }
</script>

<template>
  <div>
    <div class="block">
      <h2 class="block__title">发起同步</h2>
      <div class="form-grid">
        <div class="form-item">
          <label>同步模式</label>
          <select v-model="mode">
            <option value="m1">SSH 直推（默认）</option>
            <option value="m2">中转 registry</option>
          </select>
        </div>
        <div class="form-item" v-if="mode === 'm2'">
          <label>中转 registry</label>
          <select v-model="selectedRelay">
            <option value="">请选择</option>
            <option v-for="r in relays" :key="r.id" :value="r.id">{{ r.name }}</option>
          </select>
        </div>
        <div class="form-item span-2">
          <label>镜像清单</label>
          <div v-if="!images.length" class="form-hint">
            暂无可选镜像。请先在「镜像源」页面登记镜像清单。
          </div>
          <div v-else class="check-group">
            <div v-for="[rid, group] in images" :key="rid" class="check-group__section">
              <div class="form-hint">{{ group.name }}</div>
              <label v-for="img in group.items" :key="img.id" class="check-item">
                <input type="checkbox" :value="img.id" v-model="selectedImages" />
                <span class="num">{{ img.ref }}</span>
              </label>
            </div>
          </div>
        </div>
        <div class="form-item span-2" v-if="mode === 'm1'">
          <label>目标服务器</label>
          <div v-if="!servers.length" class="form-hint">暂无服务器。请先在「服务器」页面登记。</div>
          <div v-else class="check-group">
            <label v-for="s in servers" :key="s.id" class="check-item">
              <input type="checkbox" :value="s.id" v-model="selectedServers" />
              <span>{{ s.name }}（{{ s.host }}）</span>
            </label>
          </div>
        </div>
      </div>
      <div v-if="formErr" class="form-hint" style="color: var(--color-danger)">{{ formErr }}</div>
      <div class="toolbar" style="margin-top: 12px">
        <button class="btn btn--primary" :disabled="creating" @click="submit">
          {{ creating ? '创建中…' : '开始同步' }}
        </button>
      </div>
    </div>

    <div class="block">
      <div class="toolbar">
        <h2 class="block__title" style="margin: 0">任务历史</h2>
        <span class="spacer"></span>
        <select v-model="statusFilter" @change="page = 1; load()">
          <option value="">全部状态</option>
          <option value="running">运行中</option>
          <option value="success">成功</option>
          <option value="failed">失败</option>
          <option value="canceled">已取消</option>
        </select>
      </div>
      <div v-if="!rows.length" class="empty">暂无任务</div>
      <table v-else class="table">
        <thead>
          <tr><th>任务</th><th>模式</th><th>状态</th><th>镜像</th><th>传输</th><th>开始时间</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="t in rows" :key="t.id">
            <td class="num">{{ t.id }}</td>
            <td>{{ t.mode === 'm1' ? 'SSH 直推' : '中转' }}</td>
            <td><span class="tag" :class="statusTag(t.status)">{{ statusText(t.status) }}</span></td>
            <td>{{ (t.image_refs || []).length }}</td>
            <td class="num">{{ fmtBytes(t.bytes) }}</td>
            <td>{{ (t.started_at || t.created_at || '').replace('T', ' ').slice(0, 19) }}</td>
            <td>
              <button class="btn btn--small" @click="openDetail(t.id)">详情</button>
              <button v-if="t.status === 'running' || t.status === 'queued'" class="btn btn--small btn--danger" @click="handleCancel(t.id)">取消</button>
            </td>
          </tr>
        </tbody>
      </table>
      <div class="toolbar" v-if="total > pageSize">
        <span class="form-hint">共 {{ total }} 条</span>
        <span class="spacer"></span>
        <button class="btn btn--small" :disabled="page <= 1" @click="page--; load()">上一页</button>
        <span class="form-hint">{{ page }} / {{ totalPages() }}</span>
        <button class="btn btn--small" :disabled="page >= totalPages()" @click="page++; load()">下一页</button>
      </div>
    </div>

    <div v-if="detail" class="dialog-mask" @click.self="closeDetail">
      <div class="dialog" style="width: min(860px, calc(100vw - 32px))">
        <h3 class="dialog__title">
          任务 {{ detail.id }}
          <span class="tag" :class="statusTag(detail.status)" style="margin-left: 8px">{{ statusText(detail.status) }}</span>
        </h3>
        <div class="progress" style="margin-bottom: 12px">
          <div class="progress__bar" :class="{ 'progress__bar--done': stepProgress === 100 }" :style="{ width: stepProgress + '%' }"></div>
        </div>
        <table class="table">
          <thead>
            <tr><th>镜像</th><th>目标</th><th>状态</th><th>传输</th><th>说明</th></tr>
          </thead>
          <tbody>
            <tr v-for="(s, i) in detail.steps" :key="i">
              <td class="num">{{ s.image_ref }}</td>
              <td>{{ s.target }}</td>
              <td><span class="tag" :class="statusTag(s.status)">{{ statusText(s.status) }}</span></td>
              <td class="num">{{ fmtBytes(s.bytes) }}</td>
              <td>{{ s.error_code ? errorText(s.error_code) : (s.message || '—') }}</td>
            </tr>
          </tbody>
        </table>
        <h3 class="dialog__title" style="margin-top: 20px">日志</h3>
        <div class="log-view">
          <div v-if="!logs.length" style="color: var(--color-text-dim)">暂无日志</div>
          <div v-for="l in logs" :key="l.seq" :class="{ warn: l.level === 'warn' }">[{{ l.time }}] {{ l.line }}</div>
        </div>
        <div class="dialog__actions">
          <button v-if="detail.status === 'running'" class="btn btn--danger" @click="handleCancel(detail.id)">取消任务</button>
          <button class="btn" @click="closeDetail">关闭</button>
        </div>
      </div>
    </div>
    <div v-if="toast" class="toast">{{ toast }}</div>
  </div>
</template>

<style scoped>
.check-group { display: flex; flex-wrap: wrap; gap: var(--space-4); }
.check-group__section { min-width: 260px; }
.check-item { display: flex; align-items: center; gap: var(--space-2); padding: var(--space-1) 0; cursor: pointer; }
</style>
