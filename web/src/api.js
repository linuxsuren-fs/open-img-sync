// 唯一的 HTTP 封装：超时、网络错误包装、错误信封解析、按资源导出。
const TIMEOUT_MS = 15000

export class ApiError extends Error {
  constructor(message, code) {
    super(message)
    this.code = code
  }
}

async function request(method, path, body) {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), TIMEOUT_MS)
  let resp
  try {
    resp = await fetch(path, {
      method,
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
      signal: controller.signal,
    })
  } catch (err) {
    clearTimeout(timer)
    if (err.name === 'AbortError') {
      throw new ApiError('请求超时，请检查网络后重试', 'timeout')
    }
    throw new ApiError('无法连接后端服务，请确认服务已启动', 'network')
  }
  clearTimeout(timer)
  if (resp.status === 204) return null
  let payload = null
  try {
    payload = await resp.json()
  } catch {
    /* 非 JSON 容错 */
  }
  if (!resp.ok) {
    // 后端错误信封：{code, message}
    const message = payload && payload.message ? payload.message : `请求失败（HTTP ${resp.status}）`
    throw new ApiError(message, payload && payload.code)
  }
  return payload
}

const id = (v) => encodeURIComponent(v)

// ---- 系统信息 ----
export const getVersion = () => request('GET', '/api/version')
export const getSystemInfo = () => request('GET', '/api/system/info')

// ---- 服务器 ----
export const listServers = (params = {}) =>
  request('GET', `/api/servers?page=${params.page || 1}&page_size=${params.page_size || 20}` +
    (params.name ? `&name=${id(params.name)}` : '') + (params.tag ? `&tag=${id(params.tag)}` : ''))
export const createServer = (s) => request('POST', '/api/servers', s)
export const updateServer = (sid, s) => request('PUT', `/api/servers/${id(sid)}`, s)
export const deleteServer = (sid) => request('DELETE', `/api/servers/${id(sid)}`)
export const testServer = (sid) => request('POST', `/api/servers/${id(sid)}/test`)
export const exportServers = () => request('GET', '/api/servers/export')
export const importServers = (data) => request('POST', '/api/servers/import', data)

// ---- Registry 与镜像清单 ----
export const listRegistries = (params = {}) =>
  request('GET', `/api/registries?page=${params.page || 1}&page_size=${params.page_size || 20}` +
    (params.name ? `&name=${id(params.name)}` : '') + (params.role ? `&role=${id(params.role)}` : ''))
export const createRegistry = (r) => request('POST', '/api/registries', r)
export const updateRegistry = (rid, r) => request('PUT', `/api/registries/${id(rid)}`, r)
export const deleteRegistry = (rid) => request('DELETE', `/api/registries/${id(rid)}`)
export const testRegistry = (rid) => request('POST', `/api/registries/${id(rid)}/test`)
export const batchImages = (rid, refs) => request('POST', `/api/registries/${id(rid)}/images/batch`, { refs })
export const listImages = (rid, page = 1) =>
  request('GET', `/api/registries/${id(rid)}/images?page=${page}&page_size=20`)
export const deleteImage = (iid) => request('DELETE', `/api/images/${id(iid)}`)

// ---- 任务 ----
export const createTask = (t) => request('POST', '/api/tasks', t)
export const listTasks = (params = {}) =>
  request('GET', `/api/tasks?page=${params.page || 1}&page_size=${params.page_size || 20}` +
    (params.status ? `&status=${id(params.status)}` : ''))
export const getTask = (tid) => request('GET', `/api/tasks/${id(tid)}`)
export const cancelTask = (tid) => request('POST', `/api/tasks/${id(tid)}/cancel`)
export const getTaskLogs = (tid, since = 0) =>
  request('GET', `/api/tasks/${id(tid)}/logs?since=${since}`)

// ---- 错误码 → 文案映射 ----
export const ERROR_MESSAGES = {
  0: '成功',
  1001: '阿里云 AccessKey 无效，请检查 AK 配置',
  1002: 'AccessKey 未授权该实例，请检查 RAM 策略',
  1003: '阿里云接口限流，请稍后重试',
  1004: '临时凭据获取失败',
  2001: '服务器不可达，请检查地址与网络',
  2002: 'SSH 认证失败，请检查账号或私钥',
  2003: '目标机未安装 Docker 或不可用',
  2004: '连接超时',
  3001: 'registry 网络不可达',
  3002: 'registry 认证失败，请检查凭据',
  3003: '目标不是 registry 服务',
  3004: '镜像引用不合法',
  4001: '源 registry 认证失败',
  4002: '传输中断，请重试同步任务',
  4003: '目标机磁盘已满',
  4004: '目标机加载镜像失败',
  4005: '任务已取消',
  4006: '部分目标失败，查看步骤详情',
  4007: '推送中转 registry 失败',
  5001: '系统内部错误，请查看服务日志',
  5002: '请求参数不合法',
  5003: '资源不存在',
  5004: '未授权的请求，请检查访问令牌',
  5005: '主密钥不可用，请检查服务配置',
}

export function errorText(code, fallback) {
  if (code === undefined || code === null || code === '') return fallback || '未知错误'
  return ERROR_MESSAGES[code] || `未知错误（错误码 ${code}），请查看服务日志`
}
