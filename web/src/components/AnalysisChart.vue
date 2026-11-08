<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, MarkLineComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { MetricView } from '@/api/types'

echarts.use([LineChart, GridComponent, MarkLineComponent, TooltipComponent, CanvasRenderer])

const props = defineProps<{ metric: MetricView }>()

const el = ref<HTMLDivElement | null>(null)
let chart: echarts.ECharts | null = null

function timeLabel(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) {
    return iso
  }
  return date.toLocaleTimeString('zh-CN', { hour12: false })
}

function render() {
  if (!el.value) {
    return
  }
  if (!chart) {
    chart = echarts.init(el.value)
  }
  const samples = props.metric.samples ?? []
  chart.setOption(
    {
      animationDuration: 300,
      grid: { left: 40, right: 16, top: 16, bottom: 26 },
      tooltip: {
        trigger: 'axis',
        backgroundColor: '#101827',
        borderColor: '#2a3e60',
        textStyle: { color: '#e8eef8', fontSize: 12 },
      },
      xAxis: {
        type: 'category',
        boundaryGap: false,
        data: samples.map((sample) => timeLabel(sample.time)),
        axisLine: { lineStyle: { color: '#24344f' } },
        axisLabel: { color: '#72829c', fontSize: 10 },
      },
      yAxis: {
        type: 'value',
        scale: true,
        splitLine: { lineStyle: { color: 'rgba(36, 52, 79, 0.5)' } },
        axisLabel: { color: '#72829c', fontSize: 10 },
      },
      series: [
        {
          type: 'line',
          smooth: true,
          symbol: 'circle',
          symbolSize: 5,
          lineStyle: { color: '#4f8cff', width: 2 },
          areaStyle: { color: 'rgba(79, 140, 255, 0.12)' },
          data: samples.map((sample) => ({
            value: sample.value,
            itemStyle: { color: sample.passed ? '#31c48d' : '#f05365' },
          })),
          // 阈值参考线：一眼看出哪些点越过了门禁
          markLine: {
            silent: true,
            symbol: 'none',
            label: {
              formatter: `阈值 ${props.metric.comparison === 'ge' ? '≥' : '≤'} ${props.metric.threshold}`,
              color: '#f5a524',
              fontSize: 10,
            },
            lineStyle: { color: '#f5a524', type: 'dashed', width: 1 },
            data: [{ yAxis: props.metric.threshold }],
          },
        },
      ],
    },
    { notMerge: true },
  )
}

function onResize() {
  chart?.resize()
}

onMounted(() => {
  render()
  window.addEventListener('resize', onResize)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', onResize)
  chart?.dispose()
  chart = null
})

watch(() => props.metric, render, { deep: true })
</script>

<template>
  <div class="chart-card">
    <div class="chart-head">
      <strong>{{ metric.name }}</strong>
      <span class="threshold">阈值 {{ metric.comparison === 'ge' ? '≥' : '≤' }} {{ metric.threshold }}</span>
    </div>
    <div ref="el" class="chart-box"></div>
    <div v-if="!metric.samples || metric.samples.length === 0" class="empty" style="padding: 6px 0">
      暂无采样数据，等待 Operator 采集…
    </div>
  </div>
</template>