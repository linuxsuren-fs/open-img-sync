<script setup>
import { ref, reactive, onMounted } from 'vue'
import { listServers, createServer, updateServer, deleteServer, testServer, exportServers, importServers, errorText } from '../api.js'
import ServerDialog from '../components/ServerDialog.vue'

const rows = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const filterName = ref('')
const filterTag = ref('')
const testing = ref({})
const testResults = reactive({})
const dialogVisible = ref(false)
const editing = ref(null)
const toast = ref('')
const importResult = ref('')

async function load() {
  try {
    const res = await listServers({ page: page.value, page_size: pageSize, name: filterName.value, tag: filterTag.value })
    rows.value = Array.isArray(res.data) ? res.data : []
    total.value = res.total || 0
  } catch (e) {
    toast.value = e.message
  }
}
onMounted(load)

function openCreate() {
  editing.value = null
  dialogVisible.value = true
}
function openEdit(row) {
  editing.value = row
  dialogVisible.value = true
}
async function handleSaved(msg) {
  dialogVisible.value = false
  toast.value = msg
  await load()
  setTimeout(() => { toast.value = '' }, 2500)
}
async function handleRemove(row) {
  if (!window.confirm(`确定删除服务器「${row.name}」？`)) return
  try {
    await deleteServer(row.id)
    toast.value = '已删除'
    await load()
    setTimeout(() => { toast.value = '' }, 2500)
  } catch (e) {
    toast.value = errorText(e.code, e.message)
  }
}
async function handleTest(row) {
  testing.value[row.id] = true
  try {
    const res = await testServer(row.id)
    testResults[row.id] = res
  } catch (e) {
    testResults[row.id] = { code: e.code, message: e.message }
  } finally {
    testing.value[row.id] = false
  }
}
async function handleExport() {
  try {
    const data = await exportServers()
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = 'open-img-sync-servers.json'
    a.click()
    URL.revokeObjectURL(a.href)
  } catch (e) {
    toast.value = e.message
  }
}
function handleImportFile(ev) {
  const file = ev.target.files[0]
  if (!file) return
  const reader = new FileReader()
  reader.onload = async () => {
    try {
      const parsed = JSON.parse(reader.result)
      const res = await importServers(parsed)
      const okCount = (res.data || []).filter(i => i.ok).length
      importResult.value = `导入完成：成功 ${okCount} 条`
      await load()
    } catch (e) {
      importResult.value = errorText(e.code, e.message)
    }
  }
  reader.readAsText(file)
  ev.target.value = ''
}
function totalPages() {
  return Math.max(1, Math.ceil(total.value / pageSize))
}
</script>

<template>
  <div>
    <div class="block">
      <div class="toolbar">
        <input v-model="filterName" placeholder="按名称筛选" @keyup.enter="page = 1; load()" />
        <input v-model="filterTag" placeholder="按标签筛选" @keyup.enter="page = 1; load()" />
        <button class="btn btn--small" @click="page = 1; load()">筛选</button>
        <span class="spacer"></span>
        <button class="btn btn--small" @click="handleExport">导出</button>
        <label class="btn btn--small">
          导入
          <input type="file" accept="application/json" style="display:none" @change="handleImportFile" />
        </label>
        <button class="btn btn--primary" @click="openCreate">新建服务器</button>
      </div>
      <div v-if="importResult" class="form-hint">{{ importResult }}</div>
      <div v-if="!rows.length" class="empty">暂无服务器。点击右上角「新建服务器」开始。</div>
      <table v-else class="table">
        <thead>
          <tr><th>名称</th><th>地址</th><th>端口</th><th>账号</th><th>认证方式</th><th>凭据</th><th>标签</th><th>连通性</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.id">
            <td>{{ row.name }}</td>
            <td class="num">{{ row.host }}</td>
            <td class="num">{{ row.port }}</td>
            <td>{{ row.username }}</td>
            <td>{{ row.auth_type === 'password' ? '密码' : '私钥' }}</td>
            <td><span v-if="row.secret_mask" class="num">{{ row.secret_mask }}</span><span v-else class="form-hint">未设置</span></td>
            <td>{{ (row.tags || []).join('、') || '—' }}</td>
            <td>
              <span v-if="testResults[row.id]" :class="{ 'tag--success': testResults[row.id].code === 0 && testResults[row.id].reachable, 'tag--failed': testResults[row.id].code !== 0 }" class="tag">
                {{ errorText(testResults[row.id].code) }}
              </span>
              <span v-else class="form-hint">未测试</span>
            </td>
            <td>
              <button class="btn btn--small" :disabled="testing[row.id]" @click="handleTest(row)">
                {{ testing[row.id] ? '测试中…' : '测试' }}
              </button>
              <button class="btn btn--small" @click="openEdit(row)">编辑</button>
              <button class="btn btn--small btn--danger" @click="handleRemove(row)">删除</button>
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

    <ServerDialog
      v-model:visible="dialogVisible"
      :editing="editing"
      @saved="handleSaved"
    />
    <div v-if="toast" class="toast">{{ toast }}</div>
  </div>
</template>
