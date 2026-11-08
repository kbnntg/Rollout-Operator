export interface UserInfo {
    name: string
    role: 'admin' | 'viewer' | string
}

export interface LoginResponse {
    token: string
    user: UserInfo
    expiresAt: string
}

export interface SampleView {
    time: string
    value: number
    passed: boolean
}

export interface MetricView {
    name: string
    query: string
    threshold: number
    comparison: 'le' | 'ge' | string
    description?: string
    successes: number
    failures: number
    latestValue: number
    latestAt?: string
    passed: boolean
    samples: SampleView[]
}

export interface StepView {
    index: number
    kind: 'setWeight' | 'pause' | 'analysis' | string
    weight: number
    durationSeconds: number
    metrics?: string[] | null
}

export interface ConditionView {
    type: string
    status: string
    reason: string
    message: string
    lastTransitionTime: string
}

export interface RolloutSummary {
    namespace: string
    name: string
    phase: string
    message: string
    revision: number
    currentStepIndex: number
    currentWeight: number
    stepCount: number
    replicas: number
    image: string
    createdAt: string
    updatedAt: string
    analysis: MetricView[]
}

export interface PodView {
    name: string
    phase: string
    ready: string
    restarts: number
    image: string
    track: string
    podIP: string
    node: string
    startedAt: string
}

export interface DeploymentView {
    name: string
    track: string
    replicas: number
    ready: number
    available: number
    updated: number
    image: string
    generation: number
}

export interface SpecView {
    replicas: number
    port: number
    strategyType: string
    steps: StepView[]
    dynamicStableScale: boolean
    ingressHost?: string
    ingressPath?: string
    image: string
    analysisURL?: string
    analysisIntervalSec: number
    successThreshold: number
    failureThreshold: number
    metrics: MetricView[]
}

export interface RolloutDetail extends RolloutSummary {
    spec: SpecView
    conditions: ConditionView[]
    pods: PodView[]
    deployments: DeploymentView[]
    stableTemplateHash: string
    canaryTemplateHash: string
    blockedTemplateHash?: string
    stepStartedAt?: string
    lastAnalysisAt?: string
    observedGeneration: number
}

export interface AuditRecord {
    id: string
    time: string
    user: string
    role: string
    action: string
    namespace: string
    rollout: string
    detail?: string
    result: string
    remoteIP?: string
}

export interface ActionResponse {
    ok: boolean
    message: string
}

export type RolloutAction = 'promote' | 'abort' | 'pause' | 'resume' | 'retry'