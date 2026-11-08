<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import type { ActionResponse, RolloutAction, RolloutDetail } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { useLiveEvents, type RolloutEventPayload } from '@/composables/useLiveEvents'
import { errorMessage, formatTime, shortHash } from '@/utils/format'
import PhaseBadge from '@/components/PhaseBadge.vue'
import WeightGauge from '@/components/WeightGauge.vue'
import StepTimeline from '@/components/StepTimeline.vue'
import AnalysisChart from '@/components/AnalysisChart.vue'
import PodsTable from '@/components/PodsTable.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'

const props = defineProps<{ namespace: string; name: string }>()

const router = useRouter()
const auth = useAuthStore()

const detail = ref<RolloutDetail | null>(null)
const loading = ref(true)
const error = ref('')
const notice = ref('')
const busy = ref(false)
const pendingAction = ref<RolloutAction | null>(null)

let pollTimer: ReturnType<typeof setInterval> | undefined
let debounceTimer: ReturnType<typeof setTimeout> | undefined

const ACTION_META: Record<RolloutAction, { title: string; message: string; confirm: string; danger?: boolean }> = {
  promote: {
    title: '立即全量发布',
    message: '所有流量将切换到金丝雀版本，并结束本次灰度。确定执行吗？',
    confirm: '确认发布',
  },
  abort: {
    title: '中止并回滚',
    message: '将删除金丝雀版本、回滚到稳定版本，本次发布记录标记为中止。确定执行吗？',
    confirm: '确认回滚',
    danger: true,
  },
  pause: {
    title: '暂停发布',
    message: '发布流程会暂停在当前步骤，直到手动恢复。',
    confirm: '暂停',
  },
  resume: {
    title: '恢复发布',
    message: '继续执行后续发布步骤。',
    confirm: '恢复',
  },
  retry: {
    title: '重试发布',
    message: '将以当前 Pod 模板重新发起一次金丝雀发布。',
    confirm: '重试',
  },
}

const pendingMeta = computed(() =>
  pendingAction.value ? ACTION_META[pendingAction.value] : { title: '', message: '', confirm: '', danger: false },
)

async function load() {
  try {
    const { data } = await api.get<RolloutDetail>(
      `/rollouts/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}`,
    )
    detail.value = data
    error.value = ''
  } catch (err) {
    error.value = errorMessage(err, '加载 Rollout 详情失败')
  } finally {
    loading.value = false
  }
}

function scheduleReload() {
  if (debounceTimer) {
    clearTimeout(debounceTimer)
  }
  debounceTimer = setTimeout(load, 350)
}

const { connected } = useLiveEvents((payload: RolloutEventPayload) => {
  const meta = payload.object?.metadata
  if (meta?.namespace === props.namespace && meta?.name === props.name) {
    scheduleReload()
  }
})

onMounted(() => {
  load()
  pollTimer = setInterval(load, 10000)
})

onBeforeUnmount(() => {
  if (pollTimer) {
    clearInterval(pollTimer)
  }
  if (debounceTimer) {
    clearTimeout(debounceTimer)
  }
})

const isAdmin = computed(() => auth.isAdmin)
const canaryActive = computed(() => !!detail.value?.canaryTemplateHash)

// 按钮可用性完全由「角色 + 阶段 + 是否有金丝雀」推导
const actions = computed(() => {
  const phase = detail.value?.phase ?? ''
  const active = canaryActive.value
  return {
    promote: isAdmin.value && active && (phase === 'Progressing' || phase === 'Paused'),
    pause: isAdmin.value && active && phase === 'Progressing',
    resume: isAdmin.value && phase === 'Paused',
    abort: isAdmin.value && active && phase !== 'Failed' && phase !== 'Aborted',
    retry: isAdmin.value && !active && (phase === 'Failed' || phase === 'Aborted'),
  }
})

const stepText = computed(() => {
  const current = detail.value
  if (!current || current.currentStepIndex < 0 || current.spec.steps.length === 0) {
    return '—'
  }
  return `${current.currentStepIndex + 1} / ${current.spec.steps.length}`
})

function ask(action: RolloutAction) {
  notice.value = ''
  pendingAction.value = action
}

async function confirmAction() {
  const action = pendingAction.value
  if (!action) {
    return
  }
  busy.value = true
  try {
    const { data } = await api.post<ActionResponse>(
      `/rollouts/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/actions`,
      { action },
    )
    notice.value = data.message
    pendingAction.value = null
    await load()
  } catch (err) {
    error.value = errorMessage(err, '操作下发失败')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="page">
    <div class="back-row">
      <button class="back-btn" type="button" @click="router.push('/rollouts')">← 返回看板</button>
      <span class="live" :class="{ on: connected }">
        <span class="pulse"></span>
        {{ connected ? 'SSE 实时' : '重连中…' }}
      </span>
      <span v-if="detail" class="muted" style="margin-left: auto; font-size: 12px">
        上次分析：{{ detail.lastAnalysisAt ? formatTime(detail.lastAnalysisAt) : '—' }}
      </span>
    </div>

    <div v-if="error" class="error-banner">{{ error }}</div>
    <div v-if="notice" class="success-banner">{{ notice }}</div>

    <div v-if="loading && !detail" class="card">加载中…</div>

    <template v-if="detail">
      <section class="card detail-head">
        <div class="detail-title">
          <h1>{{ detail.name }}</h1>
          <span class="ns">{{ detail.namespace }}</span>
          <PhaseBadge :phase="detail.phase" />
          <span class="chip">{{ detail.message || '等待 Operator 调谐' }}</span>
        </div>
        <div class="detail-meta">
          <span>镜像 <span class="mono">{{ detail.image || '—' }}</span></span>
          <span>副本 <span class="mono">{{ detail.replicas }}</span></span>
          <span>稳定版本 <span class="mono">r{{ detail.revision || 1 }}</span></span>
          <span>当前步骤 <span class="mono">{{ stepText }}</span></span>
          <span>创建时间 {{ formatTime(detail.createdAt) }}</span>
        </div>
        <div class="detail-actions">
          <button class="btn success" type="button" :disabled="!actions.promote" @click="ask('promote')">
            立即发布
          </button>
          <button
            v-if="detail.phase !== 'Paused'"
            class="btn amber"
            type="button"
            :disabled="!actions.pause"
            @click="ask('pause')"
          >
            暂停
          </button>
          <button v-else class="btn" type="button" :disabled="!actions.resume" @click="ask('resume')">
            恢复
          </button>
          <button class="btn danger" type="button" :disabled="!actions.abort" @click="ask('abort')">
            中止回滚
          </button>
          <button
            v-if="detail.phase === 'Failed' || detail.phase === 'Aborted'"
            class="btn"
            type="button"
            :disabled="!actions.retry"
            @click="ask('retry')"
          >
            重试发布
          </button>
          <span v-if="!isAdmin" class="muted" style="align-self: center; font-size: 12px">
            只读账号：操作按钮不可用
          </span>
        </div>
      </section>

      <div class="grid-2">
        <div class="col-main">
          <section class="card">
            <h2>发布步骤</h2>
            <StepTimeline
              :steps="detail.spec.steps"
              :current-step-index="detail.currentStepIndex"
              :phase="detail.phase"
            />
          </section>

          <section class="card">
            <h2>金丝雀指标分析</h2>
            <div v-if="!detail.analysis.length" class="empty">该 Rollout 未配置指标分析</div>
            <AnalysisChart v-for="metric in detail.analysis" :key="metric.name" :metric="metric" />
          </section>

          <section class="card">
            <h2>Pod 实例</h2>
            <PodsTable :pods="detail.pods" />
          </section>

          <section class="card">
            <h2>Deployment 状态</h2>
            <table class="table">
              <thead>
                <tr>
                  <th>名称</th>
                  <th>轨道</th>
                  <th>副本</th>
                  <th>就绪</th>
                  <th>可用</th>
                  <th>已更新</th>
                  <th>镜像</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="deployment in detail.deployments" :key="deployment.name">
                  <td class="mono">{{ deployment.name }}</td>
                  <td>
                    <span class="chip" :class="deployment.track === 'canary' ? 'track-canary' : 'track-stable'">
                      {{ deployment.track === 'canary' ? '金丝雀' : '稳定' }}
                    </span>
                  </td>
                  <td class="mono">{{ deployment.replicas }}</td>
                  <td class="mono">{{ deployment.ready }}</td>
                  <td class="mono">{{ deployment.available }}</td>
                  <td class="mono">{{ deployment.updated }}</td>
                  <td
                    class="mono dim"
                    style="max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap"
                  >
                    {{ deployment.image }}
                  </td>
                </tr>
                <tr v-if="detail.deployments.length === 0">
                  <td colspan="7" class="empty">暂无 Deployment</td>
                </tr>
              </tbody>
            </table>
          </section>
        </div>

        <aside class="col-side">
          <section class="card">
            <h2>流量权重</h2>
            <WeightGauge :weight="detail.currentWeight" :canary-active="canaryActive" />
          </section>

          <section class="card">
            <h2>指标状态</h2>
            <div class="metric-list">
              <div
                v-for="metric in detail.analysis"
                :key="metric.name"
                class="metric-row"
                :class="metric.samples.length ? (metric.passed ? 'pass' : 'fail') : 'nodata'"
              >
                <div class="metric-head">
                  <span class="metric-name">{{ metric.name }}</span>
                  <span class="metric-value">
                    {{ metric.samples.length ? metric.latestValue : '无采样' }}
                  </span>
                </div>
                <div class="metric-sub">
                  <span>阈值 {{ metric.comparison === 'ge' ? '≥' : '≤' }} {{ metric.threshold }}</span>
                  <span v-if="metric.successes + metric.failures > 0">
                    成功 {{ metric.successes }} / 失败 {{ metric.failures }}
                  </span>
                  <span v-else-if="metric.samples.length > 0">已采样 {{ metric.samples.length }} 次</span>
                  <span v-else>等待采样</span>
                </div>
                <span class="query" :title="metric.query">{{ metric.query }}</span>
              </div>
              <div v-if="!detail.analysis.length" class="empty">未配置分析指标</div>
            </div>
          </section>

          <section class="card">
            <h2>状态条件</h2>
            <div class="cond-list">
              <div v-for="condition in detail.conditions" :key="condition.type" class="cond">
                <div class="cond-top">
                  <strong>{{ condition.type }}</strong>
                  <span class="mini-badge" :class="condition.status.toLowerCase()">
                    {{ condition.status }}
                  </span>
                </div>
                <small>{{ condition.reason }} · {{ condition.message }}</small>
                <small>{{ formatTime(condition.lastTransitionTime) }}</small>
              </div>
              <div v-if="!detail.conditions.length" class="empty">暂无状态条件</div>
            </div>
          </section>

          <section class="card">
            <h2>版本信息</h2>
            <div class="kv">
              <div class="kv-row">
                <span class="k">稳定模板</span>
                <span class="v">{{ shortHash(detail.stableTemplateHash) }}</span>
              </div>
              <div class="kv-row">
                <span class="k">金丝雀模板</span>
                <span class="v">{{ shortHash(detail.canaryTemplateHash) }}</span>
              </div>
              <div class="kv-row">
                <span class="k">阻塞模板</span>
                <span class="v">{{ shortHash(detail.blockedTemplateHash) }}</span>
              </div>
              <div class="kv-row">
                <span class="k">Ingress</span>
                <span class="v">{{ detail.spec.ingressHost || '—' }}{{ detail.spec.ingressPath || '' }}</span>
              </div>
              <div class="kv-row">
                <span class="k">指标源</span>
                <span class="v">{{ detail.spec.analysisURL || '未配置' }}</span>
              </div>
              <div class="kv-row">
                <span class="k">采样间隔</span>
                <span class="v">{{ detail.spec.analysisIntervalSec || '—' }}s</span>
              </div>
              <div class="kv-row">
                <span class="k">成功/失败阈值</span>
                <span class="v">{{ detail.spec.successThreshold }} / {{ detail.spec.failureThreshold }}</span>
              </div>
              <div class="kv-row">
                <span class="k">Observed generation</span>
                <span class="v">{{ detail.observedGeneration }}</span>
              </div>
            </div>
          </section>
        </aside>
      </div>
    </template>

    <ConfirmDialog
      :open="pendingAction !== null"
      :title="pendingMeta.title"
      :message="pendingMeta.message"
      :confirm-text="pendingMeta.confirm"
      :danger="pendingMeta.danger"
      :loading="busy"
      @confirm="confirmAction"
      @cancel="pendingAction = null"
    />
  </div>
</template>