package webhook

// 测试YAML（比如步骤回滚、Analysis定义没值、容器为空等）
import (
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kbnntg/rollout-operator/api/v1alpha1"
)

// 生成一个不存在的字段customField测测默认值，不存在的字段不会消失
func TestDefaultRawAppliesDefaultsAndKeepsUnknownFields(t *testing.T) {
	raw := []byte(`{
		"apiVersion": "delivery.sre.dev/v1alpha1",
		"kind": "Rollout",
		"metadata": {"name": "demo", "namespace": "default"},
		"spec": {
			"template": {"spec": {"containers": [{"name": "app", "image": "nginx"}]}},
			"strategy": {"type": "Canary", "canary": {"steps": [{"setWeight": 10}]}},
			"customField": "keep-me"
		}
	}`)

	patched, err := DefaultRaw(raw)
	if err != nil {
		t.Fatalf("default raw: %v", err)
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(patched, &obj); err != nil {
		t.Fatalf("unmarshal patched: %v", err)
	}
	spec := obj["spec"].(map[string]interface{})
	if spec["replicas"].(float64) != 1 {
		t.Fatalf("expected replicas default 1, got %v", spec["replicas"])
	}
	if spec["port"].(float64) != 80 {
		t.Fatalf("expected port default 80, got %v", spec["port"])
	}
	if spec["customField"] != "keep-me" {
		t.Fatalf("unknown field should be preserved")
	}
	canary := spec["strategy"].(map[string]interface{})["canary"].(map[string]interface{})
	if canary["dynamicStableScale"] != true {
		t.Fatalf("expected dynamicStableScale default true")
	}
}

// 生成一个完好的Rollout，后面基于这个测试
func validRollout() *deliveryv1alpha1.Rollout {
	replicas := int32(2)
	return &deliveryv1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		Spec: deliveryv1alpha1.RolloutSpec{
			Replicas: &replicas,
			Port:     80,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}}},
			Strategy: deliveryv1alpha1.Strategy{
				Type: deliveryv1alpha1.StrategyCanary,
				Canary: &deliveryv1alpha1.CanaryStrategy{
					Steps: []deliveryv1alpha1.Step{
						{SetWeight: int32Ptr(10)},
						{Pause: &deliveryv1alpha1.Pause{Duration: metav1.Duration{Duration: 30e9}}},
						{Analysis: &deliveryv1alpha1.AnalysisStep{}},
					},
				},
			},
			Analysis: &deliveryv1alpha1.AnalysisSpec{
				PrometheusURL:    "http://prometheus:80",
				FailureThreshold: 2,
				SuccessThreshold: 2,
				Metrics: []deliveryv1alpha1.AnalysisMetric{
					{Name: "error-rate", Query: "sum(rate(errors[1m]))", Threshold: 0.05},
				},
			},
		},
	}
}

// 接受有效值
func TestValidateAcceptsValidRollout(t *testing.T) {
	if err := Validate(validRollout()); err != nil {
		t.Fatalf("expected valid rollout, got %v", err)
	}
}

// 容器没有了
func TestValidateRejectsMissingContainers(t *testing.T) {
	ro := validRollout()
	// 把容器设置为0
	ro.Spec.Template.Spec.Containers = nil
	if err := Validate(ro); err == nil {
		t.Fatal("expected error for empty containers")
	}
}

// 决绝多个步骤，一个步骤只能有一个动作，不能边加载进度边等待时常
func TestValidateRejectsMultipleStepFields(t *testing.T) {
	ro := validRollout()
	ro.Spec.Strategy.Canary.Steps[0].Pause = &deliveryv1alpha1.Pause{Duration: metav1.Duration{Duration: 10e9}}
	if err := Validate(ro); err == nil {
		t.Fatal("expected error when a step sets both setWeight and pause")
	}
}

// SetWeight不能回退
func TestValidateRejectsDecreasingWeight(t *testing.T) {
	ro := validRollout()
	ro.Spec.Strategy.Canary.Steps = []deliveryv1alpha1.Step{
		{SetWeight: int32Ptr(50)},
		{SetWeight: int32Ptr(20)},
	}
	if err := Validate(ro); err == nil {
		t.Fatal("expected error for decreasing weight")
	}
}

// 拦截没有监控指标的
func TestValidateRejectsAnalysisWithoutSpec(t *testing.T) {
	ro := validRollout()
	ro.Spec.Analysis = nil
	if err := Validate(ro); err == nil {
		t.Fatal("expected error when analysis step exists but spec.analysis missing")
	}
}

// 拦截未知的Analysis指标
func TestValidateRejectsUnknownMetricReference(t *testing.T) {
	ro := validRollout()
	ro.Spec.Strategy.Canary.Steps[2].Analysis = &deliveryv1alpha1.AnalysisStep{Metrics: []string{"unknown"}}
	if err := Validate(ro); err == nil {
		t.Fatalf("Unknown Metrics")
	}
}

func int32Ptr(value int32) *int32 {
	return &value
}

//
