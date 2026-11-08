<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ weight: number; canaryActive: boolean }>()

const percent = computed(() => Math.max(0, Math.min(100, Math.round(props.weight))))
const stablePercent = computed(() => 100 - percent.value)
</script>

<template>
  <div class="gauge-wrap">
    <div class="gauge" :style="{ '--pct': percent }">
      <div class="gauge-inner">
        <span class="gauge-value">{{ percent }}%</span>
        <span class="gauge-label">金丝雀流量</span>
      </div>
    </div>
    <div class="gauge-legend">
      <div class="lg-row"><span class="lg-dot accent"></span>金丝雀版本 {{ percent }}%</div>
      <div class="lg-row"><span class="lg-dot muted"></span>稳定版本 {{ stablePercent }}%</div>
      <div class="lg-row">
        <span
          class="lg-dot"
          :style="{ background: canaryActive ? 'var(--purple)' : 'var(--panel-3)' }"
        ></span>
        {{ canaryActive ? '金丝雀发布进行中' : '当前无进行中的发布' }}
      </div>
    </div>
  </div>
</template>