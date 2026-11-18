<script setup>
import { ref, onMounted } from 'vue'
import { getSystemInfo, getVersion } from '../api.js'

const info = ref(null)
const version = ref(null)
const err = ref('')

onMounted(async () => {
  try {
    const [i, v] = await Promise.all([getSystemInfo(), getVersion()])
    info.value = i
    version.value = v
  } catch (e) {
    err.value = e.message
  }
})
</script>

<template>
  <div>
    <div class="block">
      <h2 class="block__title">版本信息</h2>
      <div v-if="err" class="form-hint" style="color: var(--color-danger)">{{ err }}</div>
      <table v-else-if="version" class="table">
        <tbody>
          <tr><td>版本</td><td class="num">{{ version.version }}</td></tr>
          <tr><td>提交</td><td class="num">{{ version.commit }}</td></tr>
          <tr><td>构建时间</td><td class="num">{{ version.build_time }}</td></tr>
        </tbody>
      </table>
    </div>
    <div class="block" v-if="info">
      <h2 class="block__title">运行配置</h2>
      <table class="table">
        <tbody>
          <tr><td>数据目录</td><td class="num">{{ info.data_dir }}</td></tr>
          <tr><td>主密钥来源</td><td>{{ info.master_key_source === 'env' ? '环境变量' : info.master_key_source === 'file' ? '密钥文件（首启自动生成）' : info.master_key_source }}</td></tr>
          <tr><td>访问鉴权</td><td>{{ info.auth_enabled ? '已开启 Bearer Token' : '未开启' }}</td></tr>
        </tbody>
      </table>
      <div class="form-hint" style="margin-top: 12px">
        主密钥与鉴权的配置方式见项目 README 的「安全基线」一节。
      </div>
    </div>
  </div>
</template>
