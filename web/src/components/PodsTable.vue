<script setup lang="ts">
import type { PodView } from '@/api/types'

defineProps<{ pods: PodView[] }>()

function phaseClass(phase: string): string {
  if (phase === 'Running') return 'steady'
  if (phase === 'Pending' || phase === 'ContainerCreating') return 'paused'
  return 'failed'
}
</script>

<template>
  <table class="table">
    <thead>
      <tr>
        <th>Pod</th>
        <th>轨道</th>
        <th>状态</th>
        <th>就绪</th>
        <th>重启</th>
        <th>镜像</th>
        <th>节点</th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="pod in pods" :key="pod.name">
        <td class="mono">{{ pod.name }}</td>
        <td>
          <span class="chip" :class="pod.track === 'canary' ? 'track-canary' : 'track-stable'">
            {{ pod.track === 'canary' ? '金丝雀' : '稳定' }}
          </span>
        </td>
        <td><span class="badge" :class="phaseClass(pod.phase)"><span class="dot"></span>{{ pod.phase }}</span></td>
        <td class="mono">{{ pod.ready }}</td>
        <td class="mono">{{ pod.restarts }}</td>
        <td class="mono dim" style="max-width: 220px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">
          {{ pod.image }}
        </td>
        <td class="dim">{{ pod.node || '-' }}</td>
      </tr>
      <tr v-if="pods.length === 0">
        <td colspan="7" class="empty">暂无 Pod</td>
      </tr>
    </tbody>
  </table>
</template>