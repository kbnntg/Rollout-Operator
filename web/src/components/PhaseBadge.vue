<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ phase: string }>()

const meta = computed(() => {
  const map: Record<string, { cls: string; label: string }> = {
    Steady: { cls: 'steady', label: '已稳定' },
    Progressing: { cls: 'progressing', label: '发布中' },
    Paused: { cls: 'paused', label: '已暂停' },
    Failed: { cls: 'failed', label: '已回滚' },
    Aborted: { cls: 'aborted', label: '已中止' },
    New: { cls: 'new', label: '新建' },
  }
  return map[props.phase] ?? { cls: 'new', label: props.phase || '未知' }
})
</script>

<template>
  <span class="badge" :class="meta.cls">
    <span class="dot"></span>
    {{ meta.label }}
  </span>
</template>