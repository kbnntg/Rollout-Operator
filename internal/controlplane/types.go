package controlplane

import "time"

// 这个模块就是后端DTO层（数据传输对象）接收/传送前端数据，主要与前端进行交互

// 模块一、用户验证和前端按钮
// 用户发来的账号密码
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// 用户的身份，比如有游客（viewer）、管理员（admin）
type UserInfo struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

// 拿着用户信息生成Token用于信息验证
type LoginResponse struct {
	Token    string   `json:"token"`
	User     UserInfo `json:"user"`
	ExpireAt string   `json:"expireAt"`
}

// 前端按钮操作，比如前端页面点击（全量发布）、终止按钮时，会反应给后端，后端处理完返回操作信息
type ActionRequest struct {
	Action string `json:"action"`
}

type ActionResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// 模块二、前端页面详情显示
// 列出所有Rollout
type RolloutSummary struct {
	Namespace        string       `json:"namespace"`
	Name             string       `json:"name"`
	Phase            string       `json:"phase"` // 发布状态
	Message          string       `json:"message"`
	Revision         int64        `json:"revision"`
	CurrentStepIndex int32        `json:"currentStepIndex"` // 到YAML发布步骤的哪一步了
	CurrentWeight    int32        `json:"currentWeight"`    // 当前步骤%几
	StepCount        int32        `json:"stepCount"`        // 和CurrentStepIndex展现一个进度条，比如（2/5）
	Replicas         int32        `json:"replicas"`         // 副本数
	Image            string       `json:"image"`            // 镜像名
	CreatedAt        string       `json:"createdAt"`        // 创建时间
	UpdatedAt        string       `json:"updatedAt"`        // 更新时间
	Analysis         []MetricView `json:"analysis"`         // 指标的简要状态
}

// 模块三、前端采集指标显示
const MaxSamplesPerMetric = 60 // 最多采集点
type SampleView struct {
	Time   string  `json:"time"`
	Value  float64 `json:"value"`
	Passed bool    `json:"passed"` // 展现的PromQL采集完后的SampleView中的属性
}

// 就是一些当前的指标参数，成功次数、失败次数、阈值、大于还是小于什么的
// 前端拿到 MetricView 里的Samples数组，直接丢给ECharts或者AntV画图组件，就能画出一条随着时间波动的折线图。
type MetricView struct {
	Name        string       `json:"name"`
	Query       string       `json:"query"`
	Threshold   float64      `json:"threshold"`
	Comparison  string       `json:"comparison"`
	Successes   int32        `json:"successes"`
	Failures    int32        `json:"failures"`
	LatestValue float64      `json:"latestValue"`
	LatestAt    string       `json:"latestAt,omitempty"`
	Passed      bool         `json:"passed"`
	Samples     []SampleView `json:"samples"`
}

// 金丝雀发布流程图
type StepView struct {
	Index           int32    `json:"index"`
	Kind            string   `json:"kind"` // 是setWeight/pause/analysis
	Weight          int32    `json:"weight"`
	DurationSeconds int32    `json:"durationSeconds"` // Go的Duration转成秒
	Metric          []string `json:"metrics,omitempty"`
}

// 详情，比如SPec、Pod、Deployment什么的
type RolloutDetail struct {
	RolloutSummary          // 嵌套
	Spec           SpecView `json:"spec"`
	//Conditions：直接从K8s的Conditions转换过来，展示给用户看（例如 Ready=True, Progressing=True）
	Conditions          []ConditionView  `json:"conditions"`
	Pods                []PodView        `json:"pods"`
	Deployments         []DeploymentView `json:"deployments"`
	StableTemplateHash  string           `json:"stableTemplateHash"`
	CanaryTemplateHash  string           `json:"canaryTemplateHash"`
	BlockedTemplateHash string           `json:"blockedTemplateHash"`
	StepStartedAt       string           `json:"stepStartedAt"`
	LastAnalysisAt      string           `json:"lastAnalysisAt"`
	ObservedGeneration  int64            `json:"observedGeneration"`
}

// 模块四：审计日志
type AuditRecord struct {
	ID        string `json:"id"`
	Time      string `json:"time"`
	User      string `json:"user"`
	Role      string `json:"role"`
	Action    string `json:"action"`
	Namespace string `json:"namespace"`
	Rollout   string `json:"rollout"`
	Detail    string `json:"detail,omitempty"`
	Result    string `json:"result"` // success/failed
	RemoteIP  string `json:"remoteIP,omitempty"`
	// 谁在什么时间从哪个IP对那个对象做了什么操作
}

// AuditListResponse审计日志列表
type AuditListResponse struct {
	Items []AuditRecord `json:"items"`
}

func nowStamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// PodView 金丝雀/稳定版本 Pod。
type PodView struct {
	Name      string `json:"name"`
	Phase     string `json:"phase"`
	Ready     string `json:"ready"`
	Restarts  int32  `json:"restarts"`
	Image     string `json:"image"`
	Track     string `json:"track"`
	PodIP     string `json:"podIP"`
	Node      string `json:"node"`
	StartedAt string `json:"startedAt"`
}

// DeploymentView 稳定/金丝雀 Deployment 状态。
type DeploymentView struct {
	Name       string `json:"name"`
	Track      string `json:"track"`
	Replicas   int32  `json:"replicas"`
	Ready      int32  `json:"ready"`
	Available  int32  `json:"available"`
	Updated    int32  `json:"updated"`
	Image      string `json:"image"`
	Generation int64  `json:"generation"`
}

// SpecView 详情页使用的 spec 视图。
type SpecView struct {
	Replicas            int32        `json:"replicas"`
	Port                int32        `json:"port"`
	StrategyType        string       `json:"strategyType"`
	Steps               []StepView   `json:"steps"`
	DynamicStableScale  bool         `json:"dynamicStableScale"`
	IngressHost         string       `json:"ingressHost,omitempty"`
	IngressPath         string       `json:"ingressPath,omitempty"`
	Image               string       `json:"image"`
	AnalysisURL         string       `json:"analysisURL,omitempty"`
	AnalysisIntervalSec int32        `json:"analysisIntervalSec"`
	SuccessThreshold    int32        `json:"successThreshold"`
	FailureThreshold    int32        `json:"failureThreshold"`
	Metrics             []MetricView `json:"metrics"`
}

// ConditionView 状态条件。
type ConditionView struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
	LastTransitionTime string `json:"lastTransitionTime"`
}
