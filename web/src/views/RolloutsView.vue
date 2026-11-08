<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import type { RolloutSummary } from '@/api/types'
import { useLiveEvents, type RolloutEventPayload } from '@/composables/useLiveEvents'
import { errorMessage, formatTime } from '@/utils/format'
import PhaseBadge from '@/components/PhaseBadge.vue'

const router = useRouter()

const rollouts = ref<RolloutSummary[]>([])
const loading = ref(true)
const error = ref('')
const filter = ref('')

let pollTimer: ReturnType<typeof setInterval> | undefined
let debounceTimer: ReturnType<typeof setTimeout> | undefined

async function load() {
  try {
    const { data } = await api.get<{ items: RolloutSummary[] }>('/rollouts')
    rollouts.value = data.items ?? []
    error.value = ''
  } catch (err) {
    error.value = errorMessage(err, '加载 Rollout 列表失败')
  } finally {
    loading.value = false
  }
}

// SSE 事件可能很密集，用 400ms 防抖合并刷新
function scheduleReload() {
  if (debounceTimer) {
    clearTimeout(debounceTimer)
  }
  debounceTimer = setTimeout(load, 400)
}

const { connected } = useLiveEvents((payload: RolloutEventPayload) => {
  if (payload.object?.status?.phase) {
    scheduleReload()
  }
})

onMounted(() => {
  load()
  pollTimer = setInterval(load, 15000)   // SSE 兜底轮询
})

onBeforeUnmount(() => {
  if (pollTimer) {
    clearInterval(pollTimer)
  }
  if (debounceTimer) {
    clearTimeout(debounceTimer)
  }
})

const filtered = computed(() => {
  const query = filter.value.trim().toLowerCase()
  if (!query) {
    return rollouts.value
  }
  return rollouts.value.filter((item) =>
    [item.name, item.namespace, item.image, item.phase].some((field) =>
      field.toLowerCase().includes(query),
    ),
  )
})

const stats = computed(() => ({
  total: rollouts.value.length,
  progressing: rollouts.value.filter((item) => item.phase === 'Progressing').length,
  paused: rollouts.value.filter((item) => item.phase === 'Paused').length,
  failed: rollouts.value.filter((item) => item.phase === 'Failed' || item.phase === 'Aborted').length,
}))

function open(item: RolloutSummary) {
  router.push(`/rollouts/${encodeURIComponent(item.namespace)}/${encodeURIComponent(item.name)}`)
}

function stepTag(item: RolloutSummary): string {
  if (item.currentStepIndex < 0 || item.stepCount === 0) {
    return '—'
  }
  return `${item.currentStepIndex + 1} / ${item.stepCount}`
}

function dotClass(item: RolloutSummary, index: number): string {
  if (item.currentStepIndex < 0) {
    return 'done'
  }
  if (index < item.currentStepIndex) {
    return 'done'
  }
  if (index === item.currentStepIndex) {
    return 'current'
  }
  return ''
}
</script>

<template>
  <div class="page">
    <header class="page-head">
      <div>
        <h1>发布看板</h1>
        <p>Rollout Operator · 金丝雀发布实时状态</p>
      </div>
      <div class="head-actions">
        <span class="live" :class="{ on: connected }">
          <span class="pulse"></span>
          {{ connected ? 'SSE 实时连接' : '连接重试中…' }}
        </span>
        <button class="btn ghost small" type="button" @click="load">刷新</button>
      </div>
    </header>

    <div v-if="error" class="error-banner">{{ error }}</div>

    <div class="stats">
      <div class="card stat green">
        <span class="value">{{ stats.total }}</span>
        <span class="label">Rollout 总数</span>
      </div>
      <div class="card stat accent">
        <span class="value">{{ stats.progressing }}</span>
        <span class="label">发布进行中</span>
      </div>
      <div class="card stat amber">
        <span class="value">{{ stats.paused }}</span>
        <span class="label">已暂停</span>
      </div>
      <div class="card stat red">
        <span class="value">{{ stats.failed }}</span>
        <span class="label">失败 / 中止</span>
      </div>
    </div>

    <div class="toolbar">
      <input v-model="filter" placeholder="搜索名称 / 命名空间 / 镜像" />
      <span class="muted">共 {{ filtered.length }} 项</span>
    </div>

    <div class="card" style="padding: 4px 8px">
      <table class="table">
        <thead>
          <tr>
            <th>Rollout</th>
            <th>阶段</th>
            <th>金丝雀权重</th>
            <th>步骤</th>
            <th>镜像</th>
            <th>创建时间</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in filtered" :key="`${item.namespace}/${item.name}`" @click="open(item)">
            <td>
              <div class="name-cell">
                <span class="name">{{ item.name }}</span>
                <span class="ns">{{ item.namespace }}</span>
              </div>
            </td>
            <td><PhaseBadge :phase="item.phase" /></td>
            <td>
              <div class="weight-cell">
                <div class="weight-track">
                  <div class="weight-fill" :style="{ width: `${Math.min(100, item.currentWeight)}%` }"></div>
                </div>
                <span class="weight-num">{{ item.currentWeight }}%</span>
              </div>
            </td>
            <td>
              <div style="display: flex; align-items: center; gap: 8px">
                <span class="muted mono" style="font-size: 11px">{{ stepTag(item) }}</span>
                <div class="steps-inline">
                  <span
                    v-for="index in item.stepCount"
                    :key="index"
                    class="step-dot"
                    :class="dotClass(item, index - 1)"
                  ></span>
                </div>
              </div>
            </td>
            <td class="mono dim" style="max-width: 260px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">
              {{ item.image || '—' }}
            </td>
            <td class="dim">{{ formatTime(item.createdAt) }}</td>
            <td><span class="row-link">查看 →</span></td>
          </tr>
          <tr v-if="!loading && filtered.length === 0">
            <td colspan="7" class="empty">
              {{ rollouts.length === 0 ? '暂无 Rollout：kubectl apply 一个 Rollout 试试' : '没有匹配的 Rollout' }}
            </td>
          </tr>
          <tr v-if="loading">
            <td colspan="7" class="empty">加载中…</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>