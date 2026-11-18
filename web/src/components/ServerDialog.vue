<script setup>
import { ref, watch, computed } from 'vue'
import { createServer, updateServer, errorText } from '../api.js'

const props = defineProps({
  visible: Boolean,
  editing: Object,
})
const emit = defineEmits(['update:visible', 'saved'])

const form = ref(blank())
const saving = ref(false)
const errMsg = ref('')

function blank() {
  return {
    name: '', host: '', port: 22, auth_type: 'password',
    username: 'root', secret: '', tags: '',
  }
}
watch(() => props.visible, (v) => {
  if (!v) return
  errMsg.value = ''
  if (props.editing) {
    form.value = {
      name: props.editing.name,
      host: props.editing.host,
      port: props.editing.port,
      auth_type: props.editing.auth_type,
      username: props.editing.username,
      secret: '', // 留空表示不变更
      tags: (props.editing.tags || []).join(','),
    }
  } else {
    form.value = blank()
  }
})

const title = computed(() => (props.editing ? '编辑服务器' : '新建服务器'))
const secretPlaceholder = computed(() => {
  if (props.editing) return props.editing.auth_type === 'password' ? '留空表示不修改密码' : '留空表示不修改私钥'
  return props.editing ? '' : ''
})

async function save() {
  if (!form.value.name || !form.value.host || !form.value.username) {
    errMsg.value = '请填写名称、主机地址与用户名'
    return
  }
  if (!props.editing && !form.value.secret) {
    errMsg.value = '新建时必须填写凭据'
    return
  }
  saving.value = true
  errMsg.value = ''
  const payload = {
    name: form.value.name.trim(),
    host: form.value.host.trim(),
    port: Number(form.value.port) || 22,
    auth_type: form.value.auth_type,
    username: form.value.username.trim(),
    secret: form.value.secret,
    tags: form.value.tags.split(/[,，]/).map(t => t.trim()).filter(Boolean),
  }
  try {
    if (props.editing) {
      await updateServer(props.editing.id, payload)
      emit('saved', '服务器已保存')
    } else {
      await createServer(payload)
      emit('saved', '服务器已创建')
    }
  } catch (e) {
    errMsg.value = errorText(e.code, e.message)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div v-if="visible" class="dialog-mask" @click.self="emit('update:visible', false)">
    <div class="dialog">
      <h3 class="dialog__title">{{ title }}</h3>
      <div class="form-grid">
        <div class="form-item">
          <label for="srv-name">名称</label>`n          <input id="srv-name" v-model="form.name" placeholder="如 edge-01" />
        </div>
        <div class="form-item">
          <label for="srv-host">主机地址</label>`n          <input id="srv-host" v-model="form.host" placeholder="IP 或域名" />
        </div>
        <div class="form-item">
          <label for="srv-port">SSH 端口</label>`n          <input id="srv-port" v-model.number="form.port" type="number" min="1" max="65535" />
        </div>
        <div class="form-item">
          <label>认证方式</label>
          <select v-model="form.auth_type">
            <option value="password">密码</option>
            <option value="private_key">私钥</option>
          </select>
        </div>
        <div class="form-item">
          <label for="srv-user">用户名</label>`n          <input id="srv-user" v-model="form.username" />
        </div>
        <div class="form-item">
          <label>{{ form.auth_type === 'password' ? '密码' : '私钥内容' }}</label>
          <input
            v-if="form.auth_type === 'password'"
            v-model="form.secret" type="password"
            :placeholder="editing ? '留空表示不变更' : ''" autocomplete="new-password"
          />
          <textarea
            v-else v-model="form.secret" rows="3"
            :placeholder="editing ? '留空表示不变更' : '粘贴 PEM 私钥内容'"
          ></textarea>
        </div>
        <div class="form-item span-2">
          <label for="srv-tags">标签</label>`n          <input id="srv-tags" v-model="form.tags" placeholder="多个标签用英文逗号分隔，如 prod,edge" />
        </div>
      </div>
      <div v-if="errMsg" class="form-hint" style="color: var(--color-danger)">{{ errMsg }}</div>
      <div class="dialog__actions">
        <button class="btn" @click="emit('update:visible', false)">取消</button>
        <button class="btn btn--primary" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
      </div>
    </div>
  </div>
</template>
