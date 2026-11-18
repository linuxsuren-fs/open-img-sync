<script setup>
import { ref, onMounted } from 'vue'
import { getVersion } from './api.js'
import ServersView from './views/ServersView.vue'
import RegistriesView from './views/RegistriesView.vue'
import TasksView from './views/TasksView.vue'
import OverviewView from './views/OverviewView.vue'
import SystemView from './views/SystemView.vue'

const view = ref('overview')
const views = [
  { key: 'overview', label: '总览' },
  { key: 'servers', label: '服务器' },
  { key: 'registries', label: '镜像源' },
  { key: 'tasks', label: '同步任务' },
  { key: 'system', label: '系统' },
]

const version = ref('')
onMounted(async () => {
  try {
    const v = await getVersion()
    version.value = v.version || ''
  } catch {
    version.value = ''
  }
})
</script>

<template>
  <div class="shell">
    <header class="shell__header">
      <div class="shell__brand">
        <img src="/logo.svg" alt="open-img-sync" class="shell__logo" />
        <span class="shell__name">open-img-sync</span>
        <span v-if="version" class="shell__version">{{ version }}</span>
      </div>
      <nav class="shell__nav">
        <button
          v-for="v in views" :key="v.key"
          class="shell__tab" :class="{ 'shell__tab--active': view === v.key }"
          @click="view = v.key"
        >{{ v.label }}</button>
      </nav>
    </header>
    <main class="page">
      <OverviewView v-show="view === 'overview'" :active="view === 'overview'" />
      <ServersView v-show="view === 'servers'" :active="view === 'servers'" />
      <RegistriesView v-show="view === 'registries'" :active="view === 'registries'" />
      <TasksView v-show="view === 'tasks'" :active="view === 'tasks'" />
      <SystemView v-show="view === 'system'" :active="view === 'system'" />
    </main>
  </div>
</template>

<style scoped>
.shell__header {
  background: var(--color-bg-panel);
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 0 var(--space-6);
  display: flex;
  align-items: center;
  gap: var(--space-8);
  height: 56px;
  position: sticky;
  top: 0;
  z-index: 10;
}
.shell__brand { display: flex; align-items: center; gap: var(--space-2); }
.shell__logo { width: 26px; height: 26px; }
.shell__name { font-weight: 600; font-size: 15px; }
.shell__version { color: var(--color-text-dim); font-size: 12px; font-family: var(--font-mono); }
.shell__nav { display: flex; gap: var(--space-1); }
.shell__tab {
  border: none;
  background: none;
  padding: var(--space-2) var(--space-3);
  font-size: 14px;
  color: var(--color-text-secondary);
  cursor: pointer;
  border-radius: var(--radius-sm);
}
.shell__tab:hover { color: var(--color-text-primary); background: var(--color-bg-base); }
.shell__tab--active { color: var(--color-accent); background: var(--color-accent-soft); font-weight: 500; }
</style>
