<script setup lang="ts">
defineProps<{
  open: boolean
  title: string
  message: string
  confirmText?: string
  danger?: boolean
  loading?: boolean
}>()

const emit = defineEmits<{
  (event: 'confirm'): void
  (event: 'cancel'): void
}>()
</script>

<template>
  <div v-if="open" class="modal-mask" @click.self="emit('cancel')">
    <div class="modal">
      <h3>{{ title }}</h3>
      <p>{{ message }}</p>
      <div class="modal-actions">
        <button class="btn ghost" type="button" :disabled="loading" @click="emit('cancel')">取消</button>
        <button
          class="btn"
          :class="danger ? 'danger' : ''"
          type="button"
          :disabled="loading"
          @click="emit('confirm')"
        >
          {{ loading ? '执行中…' : (confirmText || '确认') }}
        </button>
      </div>
    </div>
  </div>
</template>