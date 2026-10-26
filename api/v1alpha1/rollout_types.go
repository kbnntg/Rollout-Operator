package v1alpha1

// 这个模块是给rollout这个类型定义yaml文档中所需的字段及属性
// 先导入需要的包，
import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// 2.发布阶段的状态
type RolloutPhase string

const (
	PhaseSteady      RolloutPhase = "Steady"      // 稳定状态
	PhaseProgressing RolloutPhase = "Progressing" // 发布进行中
	PhasePaused      RolloutPhase = "Paused"      // 发布被暂停
	PhaseFailed      RolloutPhase = "Failed"      // 发布失败回滚
	PhaseAborted     RolloutPhase = "Aborted"     // 人工终止
)

// Pause发布流程中的等待步骤
type Pause struct {
	// Duration等待市场,json打成指定tag
	Duration metav1.Duration `json:"duration"` // YAML里必须写
}

// AnalysisStep表示一次指标分析步骤，只有指标通过才能发布
type AnalysisStep struct {
	// Metrics参与本步骤分析的指标列表，唯恐表示使用spec.analysis.metrics全部指标
	Metrics []string `json:"metrics,omitempty"`
}

// 4.COmparision指标与阈值比较
type Comparison string

const (
	ComparisonLE Comparison = "le" // 小于阈值通过（如错误率、延迟）
	ComparisonGE Comparison = "ge" // 大于阈值通过（如成功率）
)

// 4-1.定义AnalysisMetrics分析指标Prometheus
type AnalysisMetric struct {
	// 需要的字段：
	Name      string  `json:"name"`
	Query     string  `json:"query"`
	Threshold float64 `json:"threshold"`
	// +optional
	// +kubebuilder:default=le
	Comparison Comparison `json:"comparison,omitempty"` // 检索阈值比较
	// +optional
	Description string `json:"description,omitempty"`
}

// 4-2.AnalysisSpec 定义控制平面使用的外部指标源配置。
type AnalysisSpec struct {
	// +optional
	// +kubebuilder:default=prometheus
	Provider      string `json:"provider,omitempty"`
	PrometheusURL string `json:"prometheusURL"`
	// +optional
	Interval metav1.Duration `json:"interval,omitempty"`
	// +optional
	// +kubebuilder:default=3
	SuccessThreshold int32 `json:"successThreshold,omitempty"`
	// +optional
	// +kubebuilder:default=2
	FailureThreshold int32            `json:"failureThreshold,omitempty"`
	Metrics          []AnalysisMetric `json:"metrics"`
}

/*
	切 10% 流量 → 暂停 30 秒积累数据 → 分析错误率 → 根据结果自动决策
*/

// 5-1 定义Step发布流程，新的进来，怎么处理：需要setWeight：阈值	pause：更新过程中暂停时间积累数据	analysis：根据结果自动决策
type Step struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	SetWeight *int32        `json:"setWeight,omitempty"` // 注意首字母大写，tag 是 setWeight
	Pause     *Pause        `json:"pause,omitempty"`
	Analysis  *AnalysisStep `json:"analysis,omitempty"`
}

// 5-2 CanaryStrategy金丝雀发布策略
type CanaryStrategy struct {
	Steps []Step `json:"steps"`
	// +optional
	//+kubebuilder:default=true
	DynamicStableScale bool `json:"dynamicStableScale,omitempty"` // 按权重自动伸缩副本
}

// 5-3 BlueGreenStrategy 蓝绿发布策略
type BlueGreenStrategy struct {
	// +optional
	AutoPromotionEnabled bool `json:"autoPromotionEnabled,omitempty"` // 拼写修正为 Auto
}

// 5-4 StrategyType发布策略类型，定义灰度和金丝雀
type StrategyType string

// Type里只能填这俩值
const (
	// 金丝雀
	StrategyCanary StrategyType = "Canary"
	// 灰度发布
	StrategyBlueGreen StrategyType = "BlueGreen"
)

// 5-5 Strategy发布策略
type Strategy struct {
	Type StrategyType `json:"type"` // 从StrategyType里选俩
	// +optional
	Canary *CanaryStrategy `json:"canary,omitempty"` // 金丝雀发布，在调度Step方法的参数，type=Canary 时填
	// +optional
	BlueGreen *BlueGreenStrategy `json:"blueGreen,omitempty"` // type=BlueGreen 时填（预留）
}

/*
	strategy:
      type: Canary
      canary:
    	steps:
      	- setWeight: 10
      	- pause: { duration: 30s }
      	- analysis: { metrics: [error-rate] }
*/

// IngressSpec 对外暴露
type IngressSpec struct {
	//+optional
	Host string `json:"host,omitempty"`
	// +optional
	// +kubebuilder:default=/
	Path string `json:"path,omitempty"`
	// +optional
	ClassName string `json:"className,omitempty"`
}

// RolloutSpec的字段，用户写的
type RolloutSpec struct {
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	Replicas *int32 `json:"replicas,omitempty"`

	// +optional
	// +kubebuilder:default=80
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`

	// +optional
	Ingress  *IngressSpec           `json:"ingress,omitempty"`
	Template corev1.PodTemplateSpec `json:"template"` // 修正：PodTemplateSpec
	Strategy Strategy               `json:"strategy"`

	// +optional
	Analysis *AnalysisSpec `json:"analysis,omitempty"`
}

// 定义指标采样 弄趋势图的
type MetricSample struct {
	Time   metav1.Time `json:"time"`
	Value  float64     `json:"value"`
	Passed bool        `json:"passed"`
}

// 单个指标分析状态
type MetricAnalysisStatus struct {
	Name      string `json:"name"`
	Successes int32  `json:"successes"`
	Failures  int32  `json:"failures"`
	// 最近采样序列（最多保留60个）
	Samples []MetricSample `json:"samples,omitempty"`
}

// RolloutStatus定义仪表盘
type RolloutStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Phase RolloutPhase `json:"phase,omitempty"` // 当前状态
	// +optional
	Message string `json:"message,omitempty"` // 说明
	// +optional
	CurrentStepIndex int32 `json:"currentStepIndex,omitempty"` // 走到了第几部
	// +optional
	CurrentWeight int32 `json:"currentWeight,omitempty"` // 当前金丝雀权重
	// +optional
	Revision int64 `json:"revision,omitempty"` // 第几次发布
	// +optional
	StableTemplateHash string `json:"stableTemplateHash,omitempty"` // 稳定版模板哈希
	// +optional
	CanaryTemplateHash string `json:"canaryTemplateHash,omitempty"` // 金丝雀模板哈希
	// +optional
	BlockedTemplateHash string `json:"blockedTemplateHash,omitempty"` //失败阻塞的哈希
	// +optional
	StepStartedAt *metav1.Time `json:"stepStartedAt,omitempty"`
	// +optional
	LastAnalysisAt *metav1.Time `json:"lastAnalysisAt,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	Analysis []MetricAnalysisStatus `json:"analysis,omitempty"` // 加上 tag
}

// Rollout 是 Rollout Operator 管理的自定义资源。
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Weight",type=integer,JSONPath=`.status.currentWeight`
// +kubebuilder:printcolumn:name="Step",type=integer,JSONPath=`.status.currentStepIndex`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.template.spec.containers[0].image`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

type Rollout struct {
	metav1.TypeMeta   `json:",inline"`            // 身份证：apiVersion+kind
	metav1.ObjectMeta `json:"metadata,omitempty"` // 元数据：名字、namespace、labels
	Spec              RolloutSpec                 `json:"spec,omitempty"`   // 想要的
	Status            RolloutStatus               `json:"status,omitempty"` // 现在的
}

// RolloutList包含Rollout列表
// +kubebuilder:object:root=true
type RolloutList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Rollout `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Rollout{}, &RolloutList{})
}
