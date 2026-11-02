package analysis

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	deliveryv1alpha1 "github.com/kbnntg/rollout-operator/api/v1alpha1"
)

// Client分析器的以来指标查询接口，注入假实现
type Client interface {
	// QUery是执行PromQL，返回单个浮点结果
	Query(ctx context.Context, expr string) (float64, error)
	// ctx是上下文，expr是PromQL 表达式，原样发给 Prometheus
}

// PrometheusClient基于Prometheus HTTPAPI的指标查询
type PrometheusClient struct {
	api promv1.API
}

// NewClient创建指定地址的Prometheus客户端
func NewClient(address string) (Client, error) {
	if address == "" {
		return nil, errors.New("Prometheus address is empty")
	}
	raw, err := api.NewClient(api.Config{Address: address})
	if err != nil {
		return nil, fmt.Errorf("create prometheus client: %w", err)
	}
	return &PrometheusClient{api: promv1.NewAPI(raw)}, nil
}

// Query执行PromQL查询并返回采样值，向量结果取最大值，矩阵结果曲最后值，标量直接返回
func (c *PrometheusClient) Query(ctx context.Context, expr string) (float64, error) {
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	value, _, err := c.api.Query(qctx, expr, time.Now())
	if err != nil {
		return 0, fmt.Errorf("Prometheus query %q failed: %w", expr, err)
	}
	return extractValue(value)
}

// 创建处理结果的方法
func extractValue(value model.Value) (float64, error) {
	switch t := value.(type) {
	case *model.Scalar:
		return float64(t.Value), nil
	case model.Vector:
		if len(t) == 0 {
			return 0, errors.New("empty result vector")
		}
		// max先等于个负无穷，后面判断用
		max := math.Inf(-1)
		// 循环遍历上面query查询的语句结果，之后返回最大的
		for _, sample := range t {
			if v := float64(sample.Value); v > max {
				max = v
			}
		}
		// 兜底，看看是否对max处理
		if math.IsInf(max, -1) || math.IsNaN(max) {
			return 0, errors.New("result vector contains only NaN")
		}
		return max, nil
		/*
				model.Vector = []*model.Sample{
				{Metric: {app: "my-app", pod: "pod-1"}, Value: 100},
				{Metric: {app: "my-app", pod: "pod-2"}, Value: 200},
				{Metric: {app: "my-app", pod: "pod-3"}, Value: 150},
			}取最坏的节瓜
		*/
	case model.Matrix:
		if len(t) == 0 || len(t[0].Values) == 0 {
			return 0, errors.New("empty range result")
		}

		// 取最后一个点
		last := t[0].Values[len(t[0].Values)-1]
		return float64(last.Value), nil
	default:
		return 0, fmt.Errorf("unsupported prometheus result type %T", value)
	}
}

// Pass比较函数，将查询到的值和阈值比较
func Pass(value, threshold float64, comparison deliveryv1alpha1.Comparison) bool {
	if comparison == deliveryv1alpha1.ComparisonGE {
		return value >= threshold
	}
	return value <= threshold
}

// IntervalOF返回分析配置的采样间隔，默认10s
func IntervalOf(spec *deliveryv1alpha1.AnalysisSpec) time.Duration {
	if spec == nil || spec.Interval.Duration <= 0 {
		return 10 * time.Second
	}
	return spec.Interval.Duration
}

// SuccessThresholdOf返回成功阈值，默认3次
func SuccessThresholdOf(spec *deliveryv1alpha1.AnalysisSpec) int32 {
	if spec == nil || spec.SuccessThreshold <= 0 {
		return 3
	}
	return spec.SuccessThreshold
}

// FailureThresholdOf返回连续失败阈值，默认2次
func FailureThresholdOf(spec *deliveryv1alpha1.AnalysisSpec) int32 {
	if spec == nil || spec.FailureThreshold <= 0 {
		return 2
	}
	return spec.FailureThreshold
}

// ComparisonOf返回指标比较方向，默认1e
func ComparisonOf(metric deliveryv1alpha1.AnalysisMetric) deliveryv1alpha1.Comparison {
	if metric.Comparison == deliveryv1alpha1.ComparisonGE {
		return deliveryv1alpha1.ComparisonGE
	}
	return deliveryv1alpha1.ComparisonLE
}
