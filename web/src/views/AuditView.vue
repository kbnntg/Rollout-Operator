<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '@/api/client'
import type { AuditRecord } from '@/api/types'
import { actionLabel, errorMessage, formatTime } from '@/utils/format'

const records = ref<AuditRecord[]>([])
const loading = ref(true)
const error = ref('')
const filter = ref('')

async function load() {
  try {
    const { data } = await api.get<{ items: AuditRecord[] }>('/audit', { params: { limit: 500 } })
    records.value = data.items ?? []
    error.value = ''
  } catch (err) {
    error.value = errorMessage(err, '加载审计日志失败')
  } finally {
    loading.value = false
  }
}

onMounted(load)

const filtered = computed(() => {
  const query = filter.value.trim().toLowerCase()
  if (!query) {
    return records.value
  }
  return records.value.filter((record) =>
    [record.namespace, record.rollout, record.user, record.action].some((field) =>
      (field || '').toLowerCase().includes(query),
    ),
  )
})

function roleLabel(role: string): string {
  return role === 'admin' ? '管理员' : '只读'
}

function resultClass(result: string): string {
  return result === 'accepted' ? 'steady' : 'failed'
}
</script>

<template>
  <div class="page">
    <header class="page-head">
      <div>
        <h1>审计日志</h1>
        <p>所有控制台变更操作都会被记录，可追溯到操作人与结果</p>
      </div>
      <div class="head-actions">
        <button class="btn ghost small" type="button" @click="load">刷新</button>
      </div>
    </header>

    <div v-if="error" class="error-banner">{{ error }}</div>

    <div class="toolbar">
      <input v-model="filter" placeholder="按命名空间 / Rollout / 用户 / 操作过滤" />
      <span class="muted">共 {{ filtered.length }} 条</span>
    </div>

    <div class="card" style="padding: 4px 8px">
      <table class="table">
        <thead>
          <tr>
            <th>时间</th>
            <th>操作人</th>
            <th>角色</th>
            <th>操作</th>
            <th>Rollout</th>
            <th>结果</th>
            <th>来源</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="record in filtered" :key="record.id">
            <td class="dim">{{ formatTime(record.time) }}</td>
            <td><strong>{{ record.user }}</strong></td>
            <td class="dim">{{ roleLabel(record.role) }}</td>
            <td><span class="chip">{{ actionLabel(record.action) }}</span></td>
            <td class="mono">{{ record.namespace }}/{{ record.rollout }}</td>
            <td>
              <span class="badge" :class="resultClass(record.result)">
                <span class="dot"></span>{{ record.result === 'accepted' ? '已受理' : '失败' }}
              </span>
            </td>
            <td class="mono dim">{{ record.remoteIP || '-' }}</td>
          </tr>
          <tr v-if="loading">
            <td colspan="7" class="empty">加载中…</td>
          </tr>
          <tr v-else-if="filtered.length === 0">
            <td colspan="7" class="empty">暂无审计记录</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>