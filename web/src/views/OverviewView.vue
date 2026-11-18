<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { listTasks, errorText } from '../api.js'

const tasks = ref([])
const total = ref(0)
const loading = ref(false)
let timer = null

async function load() {
  loading.value = true
  try {
    const res = await listTasks({ page: 1, page_size: 10 })
    tasks.value = Array.isArray(res.data) ? res.data : []
    total.value = res.total || 0
  } catch {
    tasks.value = []
  } finally {
    loading.value = false
  }
}
onMounted(() => {
  load()
  timer = setInterval(load, 5000)
})
onUnmounted(() => { if (timer) clearInterval(timer) })

function statusTag(status) {
  return { success: 'tag--success', failed: 'tag--failed', running: 'tag--running', canceled: 'tag--canceled' }[status] || 'tag--pending'
}
function statusText(status) {
  return { queued: '排队中', running: '运行中', success: '成功', failed: '失败', canceled: '已取消' }[status] || status
}
function fmtBytes(n) {
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`
}
function stepSummary(t) {
  if (!t.steps || !t.steps.length) return '—'
  const done = t.steps.filter(s => ['success', 'skipped', 'failed', 'canceled'].includes(s.status)).length
  return `${done}/${t.steps.length}`
}
</script>

<template>
  <div>
    <div class="block">
      <h2 class="block__title">最近任务</h2>
      <div v-if="loading && !tasks.length" class="empty">加载中…</div>
      <div v-else-if="!tasks.length" class="empty">
        暂无同步任务。前往「镜像源」登记镜像清单，再到「同步任务」发起同步。
      </div>
      <table v-else class="table">
        <thead>
          <tr><th>任务</th><th>模式</th><th>状态</th><th>步骤</th><th>传输</th><th>结果说明</th></tr>
        </thead>
        <tbody>
          <tr v-for="t in tasks" :key="t.id">
            <td class="num">{{ t.id }}</td>
            <td>{{ t.mode === 'm1' ? 'SSH 直推' : '中转' }}</td>
            <td><span class="tag" :class="statusTag(t.status)">{{ statusText(t.status) }}</span></td>
            <td class="num">{{ stepSummary(t) }}</td>
            <td class="num">{{ fmtBytes(t.bytes) }}</td>
            <td>{{ t.code ? errorText(t.code) : (t.message || '—') }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
