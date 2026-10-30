package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// 添加Prometheus指标
var (
	// RolloutStarted是开始执行的发布总数
	RolloutsStarted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "rollout_operator", Name: "rollouts_started_total",
		Help: "Total number of rollouts started by the operator",
	}, []string{"namespace"})
	// RolloutStarted是开始执行的发布总数
	RolloutsPromoted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "rollout_operator", Name: "rollouts_promoted_total",
		Help: "Total number of rollouts promoted to stable",
	}, []string{"namespace"})

	// RolloutsRolledBack统计自动/人工回滚总数，reason区分原因，之前rollback会调用
	RolloutsRolledBack = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "rollouts_rolled_back_total", Namespace: "rollout_operator", Help: "Total number of rollouts rolled back",
	}, []string{"namespace", "reason"})

	// AnalysisSamples统计指标采样次数，passed区分通过与失败
	AnalysisSamples = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "rollout_operator", Name: "analysis_samples_total", Help: "Total number of metric analysis sample",
	}, []string{"namespace", "metric", "passed"})

	// CanaryWeight 当前各 Rollout 的金丝雀流量权重。
	CanaryWeight = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "rollout_operator", Name: "canary_weight",
		Help: "Current canary traffic weight of a rollout.",
	}, []string{"namespace", "rollout"})
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		RolloutsStarted, RolloutsPromoted, RolloutsRolledBack, AnalysisSamples, CanaryWeight)
}
