<script setup>
import { ref, onMounted } from 'vue'
import { listRegistries, createRegistry, updateRegistry, deleteRegistry, testRegistry, batchImages, listImages, deleteImage, errorText } from '../api.js'
import RegistryDialog from '../components/RegistryDialog.vue'

const rows = ref([])
const total = ref(0)
const filterRole = ref('')
const dialogVisible = ref(false)
const editing = ref(null)
const toast = ref('')
const testResults = ref({})

// 镜像清单面板
const expanded = ref(null)
const images = ref([])
const batchText = ref('')
const batchResults = ref([])
const batchOpen = ref(false)

async function load() {
  try {
    const res = await listRegistries({ page: 1, page_size: 100, role: filterRole.value })
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
async function handleSaved(msg) {
  dialogVisible.value = false
  toast.value = msg
  await load()
  setTimeout(() => { toast.value = '' }, 2500)
}
async function handleTest(row) {
  try {
    const res = await testRegistry(row.id)
    testResults.value[row.id] = res
  } catch (e) {
    testResults.value[row.id] = { code: e.code, message: e.message }
  }
}
async function handleRemove(row) {
  if (!window.confirm(`确定删除镜像源「${row.name}」？其镜像清单将一并删除。`)) return
  try {
    await deleteRegistry(row.id)
    await load()
    toast.value = '已删除'
    setTimeout(() => { toast.value = '' }, 2500)
  } catch (e) {
    toast.value = errorText(e.code, e.message)
  }
}
async function toggleImages(row) {
  if (expanded.value === row.id) {
    expanded.value = null
    return
  }
  expanded.value = row.id
  batchResults.value = []
  await loadImages(row.id)
}
async function loadImages(rid) {
  try {
    const res = await listImages(rid)
    images.value = Array.isArray(res.data) ? res.data : []
  } catch {
    images.value = []
  }
}
async function submitBatch(row) {
  if (!batchText.value.trim()) return
  try {
    const res = await batchImages(row.id, batchText.value)
    batchResults.value = Array.isArray(res.data) ? res.data : []
    batchText.value = ''
    await loadImages(row.id)
  } catch (e) {
    toast.value = errorText(e.code, e.message)
  }
}
async function removeImage(img) {
  try {
    await deleteImage(img.id)
    await loadImages(img.registry_id)
  } catch (e) {
    toast.value = errorText(e.code, e.message)
  }
}
function typeText(t) {
  return { standard: '固定凭据', 'aliyun-temp': '阿里云临时凭据', anonymous: '匿名' }[t] || t
}
function roleText(r) {
  return { source: '镜像来源', relay: '中转' }[r] || r
}
</script>

<template>
  <div>
    <div class="block">
      <div class="toolbar">
        <select v-model="filterRole" @change="load">
          <option value="">全部角色</option>
          <option value="source">镜像来源</option>
          <option value="relay">中转</option>
        </select>
        <span class="spacer"></span>
        <button class="btn btn--primary" @click="openCreate">新建镜像源</button>
      </div>
      <div v-if="!rows.length" class="empty">暂无镜像源。登记来源或中转 registry 后即可录入镜像清单。</div>
      <table v-else class="table">
        <thead>
          <tr><th>名称</th><th>地址</th><th>类型</th><th>角色</th><th>账号</th><th>凭据</th><th>连通性</th><th></th></tr>
        </thead>
        <tbody>
          <template v-for="row in rows" :key="row.id">
            <tr>
              <td>{{ row.name }}</td>
              <td class="num wrap">{{ row.address }}</td>
              <td>{{ typeText(row.type) }}</td>
              <td>{{ roleText(row.role) }}</td>
              <td>{{ row.username || (row.aliyun && row.aliyun.instance ? row.aliyun.instance : '—') }}</td>
              <td>
                <span v-if="row.secret_mask" class="num">{{ row.secret_mask }}</span>
                <span v-else-if="row.aliyun && row.aliyun.ak_mask" class="num">{{ row.aliyun.ak_mask }}</span>
                <span v-else class="form-hint">匿名</span>
              </td>
              <td class="wrap">
                <span v-if="testResults[row.id]" :class="testResults[row.id].ok ? 'tag--success' : 'tag--failed'" class="tag">
                  {{ testResults[row.id].message || errorText(testResults[row.id].code) }}
                </span>
                <span v-else class="form-hint">未测试</span>
              </td>
              <td>
                <button class="btn btn--small" @click="handleTest(row)">测试</button>
                <button class="btn btn--small" @click="editing = row; dialogVisible = true">编辑</button>
                <button class="btn btn--small btn--danger" @click="handleRemove(row)">删除</button>
                <button class="btn btn--small" @click="toggleImages(row)">
                  {{ expanded === row.id ? '收起清单' : '镜像清单' }}
                </button>
              </td>
            </tr>
            <tr v-if="expanded === row.id">
              <td colspan="8">
                <div v-if="images.length" class="toolbar">
                  <span class="form-hint">已登记 {{ images.length }} 项</span>
                </div>
                <table v-if="images.length" class="table">
                  <thead><tr><th>镜像引用</th><th>登记时间</th><th></th></tr></thead>
                  <tbody>
                    <tr v-for="img in images" :key="img.id">
                      <td class="num">{{ img.ref }}</td>
                      <td>{{ (img.created_at || '').replace('T', ' ').slice(0, 19) }}</td>
                      <td><button class="btn btn--small btn--danger" @click="removeImage(img)">移除</button></td>
                    </tr>
                  </tbody>
                </table>
                <div v-else class="form-hint">清单为空</div>
                <div class="toolbar" style="margin-top: 8px">
                  <button class="btn btn--small" @click="batchOpen = !batchOpen">
                    {{ batchOpen ? '收起' : '批量录入' }}
                  </button>
                </div>
                <div v-if="batchOpen">
                  <div class="form-item">
                    <textarea v-model="batchText" rows="4" placeholder="每行一个镜像引用，如&#10;library/nginx:1.27&#10;myapp@sha256:…"></textarea>
                  </div>
                  <button class="btn btn--primary btn--small" @click="submitBatch(row)">录入</button>
                  <div v-if="batchResults.length" style="margin-top: 8px">
                    <div v-for="r in batchResults" :key="r.line" class="form-hint">
                      第 {{ r.line }} 行：<span v-if="r.ok">{{ r.ref }} 已录入</span>
                      <span v-else style="color: var(--color-danger)">{{ r.reason }}</span>
                    </div>
                  </div>
                </div>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>

    <RegistryDialog v-model:visible="dialogVisible" :editing="editing" @saved="handleSaved" />
    <div v-if="toast" class="toast">{{ toast }}</div>
  </div>
</template>
