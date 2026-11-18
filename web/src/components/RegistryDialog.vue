<script setup>
import { ref, watch, computed } from 'vue'
import { createRegistry, updateRegistry, errorText } from '../api.js'

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
    name: '', address: '', type: 'anonymous', role: 'source',
    username: '', secret: '',
    aliyun: { region: '', instance: '', access_key_id: '', access_key_secret: '' },
  }
}
watch(() => props.visible, (v) => {
  if (!v) return
  errMsg.value = ''
  if (props.editing) {
    form.value = {
      name: props.editing.name,
      address: props.editing.address,
      type: props.editing.type,
      role: props.editing.role,
      username: props.editing.username || '',
      secret: '',
      aliyun: {
        region: (props.editing.aliyun && props.editing.aliyun.region) || '',
        instance: (props.editing.aliyun && props.editing.aliyun.instance) || '',
        access_key_id: '',
        access_key_secret: '',
      },
    }
  } else {
    form.value = blank()
  }
})

const title = computed(() => (props.editing ? '编辑镜像源' : '新建镜像源'))
const needStandard = computed(() => form.value.type === 'standard')
const needAliyun = computed(() => form.value.type === 'aliyun-temp')

async function save() {
  if (!form.value.name || !form.value.address) {
    errMsg.value = '请填写名称与地址'
    return
  }
  if (needStandard.value && !form.value.username) {
    errMsg.value = '固定凭据类型需要填写用户名'
    return
  }
  if (needStandard.value && !props.editing && !form.value.secret) {
    errMsg.value = '固定凭据类型需要填写密码或 Token'
    return
  }
  if (needAliyun.value && !props.editing && (!form.value.aliyun.access_key_id || !form.value.aliyun.access_key_secret)) {
    errMsg.value = '阿里云类型需要填写 AccessKey'
    return
  }
  saving.value = true
  errMsg.value = ''
  const payload = {
    name: form.value.name.trim(),
    address: form.value.address.trim(),
    type: form.value.type,
    role: form.value.role,
    username: form.value.username,
    secret: form.value.secret,
    aliyun: needAliyun.value ? form.value.aliyun : {},
  }
  try {
    if (props.editing) {
      await updateRegistry(props.editing.id, payload)
      emit('saved', '镜像源已保存')
    } else {
      await createRegistry(payload)
      emit('saved', '镜像源已创建')
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
          <label for="reg-name">名称</label>`n          <input id="reg-name" v-model="form.name" placeholder="如 docker-hub、aliyun-ee-hz" />
        </div>
        <div class="form-item">
          <label for="reg-addr">地址</label>`n          <input id="reg-addr" v-model="form.address" placeholder="如 registry.example.com:5443，自动补全 https://" />
        </div>
        <div class="form-item">
          <label>类型</label>
          <select v-model="form.type">
            <option value="anonymous">匿名</option>
            <option value="standard">固定凭据</option>
            <option value="aliyun-temp">阿里云临时凭据</option>
          </select>
        </div>
        <div class="form-item">
          <label>角色</label>
          <select v-model="form.role">
            <option value="source">镜像来源</option>
            <option value="relay">中转</option>
          </select>
        </div>
        <template v-if="needStandard">
          <div class="form-item">
            <label>用户名</label>
            <input v-model="form.username" />
          </div>
          <div class="form-item">
            <label>密码 / Token</label>
            <input v-model="form.secret" type="password" :placeholder="editing ? '留空表示不变更' : ''" autocomplete="new-password" />
          </div>
        </template>
        <template v-if="needAliyun">
          <div class="form-item">
            <label>地域</label>
            <input v-model="form.aliyun.region" placeholder="如 cn-hangzhou" />
          </div>
          <div class="form-item">
            <label>实例名</label>
            <input v-model="form.aliyun.instance" placeholder="如 xxx-registry" />
          </div>
          <div class="form-item">
            <label>AccessKey ID</label>
            <input v-model="form.aliyun.access_key_id" :placeholder="editing ? '留空表示不变更' : ''" />
          </div>
          <div class="form-item">
            <label>AccessKey Secret</label>
            <input v-model="form.aliyun.access_key_secret" type="password" :placeholder="editing ? '留空表示不变更' : ''" autocomplete="new-password" />
          </div>
        </template>
      </div>
      <div v-if="errMsg" class="form-hint" style="color: var(--color-danger)">{{ errMsg }}</div>
      <div class="dialog__actions">
        <button class="btn" @click="emit('update:visible', false)">取消</button>
        <button class="btn btn--primary" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
      </div>
    </div>
  </div>
</template>
