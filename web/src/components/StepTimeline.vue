<script setup lang="ts">
import type { StepView } from '@/api/types'

const props = defineProps<{ steps: StepView[]; currentStepIndex: number; phase: string }>()

function statusOf(index: number): 'done' | 'current' | 'pending' | 'failed' {
  if (props.phase === 'Failed' || props.phase === 'Aborted') {
    if (props.currentStepIndex < 0) {
      return 'pending'
    }
    if (index < props.currentStepIndex) {
      return 'done'
    }
    if (index === props.currentStepIndex) {
      return 'failed'
    }
    return 'pending'
  }
  if (props.currentStepIndex < 0) {
    return 'done'
  }
  if (index < props.currentStepIndex) {
    return 'done'
  }
  if (index === props.currentStepIndex) {
    return 'current'
  }
  return 'pending'
}

function tagOf(status: string): string {
  switch (status) {
    case 'done':
      return '完成'
    case 'current':
      return '进行中'
    case 'failed':
      return '失败'
    default:
      return '等待'
  }
}

function iconOf(kind: string): string {
  if (kind === 'setWeight') return '⇄'
  if (kind === 'pause') return '⏸'
  if (kind === 'analysis') return '📈'
  return '•'
}

function titleOf(step: StepView): string {
  if (step.kind === 'setWeight') return `调整金丝雀流量权重 → ${step.weight}%`
  if (step.kind === 'pause') return `观察等待 ${step.durationSeconds} 秒`
  if (step.kind === 'analysis') return '指标分析（Prometheus）'
  return step.kind
}

function subtitleOf(step: StepView): string {
  if (step.kind === 'setWeight') return `${step.weight}% 的流量将进入金丝雀版本`
  if (step.kind === 'pause') return '等待流量与指标稳定后进入下一步'
  if (step.kind === 'analysis') {
    return step.metrics && step.metrics.length > 0
      ? `分析指标：${step.metrics.join('、')}`
      : '分析全部指标，连续达标后继续发布'
  }
  return ''
}
</script>

<template>
  <div class="timeline">
    <div
      v-for="step in steps"
      :key="step.index"
      class="timeline-item"
      :class="statusOf(step.index)"
    >
      <span class="timeline-dot">{{ iconOf(step.kind) }}</span>
      <div class="timeline-body">
        <strong>{{ step.index + 1 }}. {{ titleOf(step) }}</strong>
        <small>{{ subtitleOf(step) }}</small>
      </div>
      <span class="timeline-tag" :class="statusOf(step.index)">
        {{ tagOf(statusOf(step.index)) }}
      </span>
    </div>
    <div v-if="!steps.length" class="empty">该 Rollout 未配置发布步骤</div>
  </div>
</template>