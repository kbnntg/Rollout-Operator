package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	jsonpatch "gomodules.xyz/jsonpatch/v2"
	admissionv1 "k8s.io/api/admission/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	deliveryv1alpha1 "github.com/kbnntg/rollout-operator/api/v1alpha1"
)

// MutatingWebhookPath用来默认值准入
const MutatingWebhookPath = "/mutate-delivery-sre-dev-v1alpha1-rollout"

// ValidatingWebhookPath校验Webhook注册路径，管yaml中属性规格的
const ValidatingWebhookPath = "/validate-delivery-sre-dev-v1alpha1-rollout"

// Register 注册 Rollout 的准入 Webhook
func Register(mgr ctrl.Manager) {
	server := mgr.GetWebhookServer()
	server.Register(MutatingWebhookPath, &webhook.Admission{Handler: &Defaulter{}})
	server.Register(ValidatingWebhookPath, &webhook.Admission{Handler: &Validator{}})
}

// Defaulter为Rollout注入默认值
type Defaulter struct {
}

// Handle实现admission.Handler
func (d *Defaulter) Handle(_ context.Context, req admission.Request) admission.Response {
	// 如果接收的是删除操作，就直接放行，不用加啥默认值
	if req.Operation == admissionv1.Delete {
		return admission.Allowed("delete is not default")
	}
	// req.Object.Raw就是接收到的yaml源字段
	patched, err := DefaultRaw(req.Object.Raw)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	// 啥也没改就等于源值
	if string(patched) == string(req.Object.Raw) {
		return admission.Allowed("no defaults applied")
	}
	patch, err := jsonpatch.CreatePatch(req.Object.Raw, patched)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.Patched("defaults applied", patch...)

}

// DefaultRaw就是接收到的Yaml字段对其进行默认值配置，返回默认值json
func DefaultRaw(raw []byte) ([]byte, error) {
	// 创建一个空map
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("decode rollout:%w", err)
	}
	// 获取传来字段的spec
	spec := childMap(obj, "spec")

	// 设置副本、端口、strategy类型、ingress默认值
	setIfMissing(spec, "replicas", float64(1))
	setIfMissing(spec, "port", float64(80))

	strategy := childMap(spec, "strategy")
	setIfMissing(strategy, "type", string(deliveryv1alpha1.StrategyCanary))
	if canaryValue, ok := strategy["canary"]; ok {
		if canary, ok := canaryValue.(map[string]interface{}); ok {
			setIfMissing(canary, "dynamicStableScale", true)
		}
	}
	if ingressValue, ok := spec["ingress"]; ok {
		if ingress, ok := ingressValue.(map[string]interface{}); ok {
			setIfMissing(ingress, "path", "/")
		}
	}
	if analysisValue, ok := spec["analysis"]; ok {
		if analysisMap, ok := analysisValue.(map[string]interface{}); ok {
			setIfMissing(analysisMap, "provider", "prometheus")
			setIfMissing(analysisMap, "interval", "10s")
			setIfMissing(analysisMap, "successThreshold", float64(3))
			setIfMissing(analysisMap, "failureThreshold", float64(2))
			if metricValues, ok := analysisMap["metrics"].([]interface{}); ok {
				for _, metricValue := range metricValues {
					if metric, ok := metricValue.(map[string]interface{}); ok {
						setIfMissing(metric, "comparison", string(deliveryv1alpha1.ComparisonLE))
					}
				}
			}
		}
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("encode rollout: %w", err)
	}
	return out, nil
}

// validator校验合法
type Validator struct {
}

// Handle事先admission.Handler
func (v *Validator) Handle(_ context.Context, req admission.Request) admission.Response {
	// 如果删除还是忽略
	if req.Operation == admissionv1.Delete {
		return admission.Allowed("delete is not validated")
	}
	var ro deliveryv1alpha1.Rollout
	if err := json.Unmarshal(req.Object.Raw, &ro); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	if err := Validate(&ro); err != nil {
		return admission.Denied(err.Error())
	}
	return admission.Allowed("valid rollout")
}

// validate返回Rollout配置的错误，nil表示合法
func Validate(ro *deliveryv1alpha1.Rollout) error {
	spec := &ro.Spec

	// 没有容器情况下
	if len(spec.Template.Spec.Containers) == 0 {
		return errors.New("spec.template.spec.containers至少需要一个容器")
	}

	// 端口校验
	if spec.Port < 0 || spec.Port > 65535 {
		return fmt.Errorf("spec.port必须1-65535之间，当前%d", spec.Port)
	}

	// 副本数不能低于0
	if spec.Replicas != nil && *spec.Replicas < 0 {
		return fmt.Errorf("spec的副本不能低于0，当前是%d", *spec.Replicas)
	}
	// ingress路径必须以/开头
	if spec.Ingress != nil && spec.Ingress.Path != "" && !strings.HasPrefix(spec.Ingress.Path, "/") {
		return fmt.Errorf("spec的ingress必须以/开头，当前为%q", spec.Ingress.Path)
	}

	// 下面是发布状态、步骤
	switch spec.Strategy.Type {
	case deliveryv1alpha1.StrategyCanary, "":
		if spec.Strategy.Canary == nil || len(spec.Strategy.Canary.Steps) == 0 {
			return errors.New("Canary策略至少配置一个步骤(spec.strategy.canary.steps)")
		}
		if err := validatesteps(spec.Strategy.Canary.Steps); err != nil {
			return err
		}
	case deliveryv1alpha1.StrategyBlueGreen:
	default:
		return fmt.Errorf("不支持的策略类型%q,只有Canary活BlueGreen", spec.Strategy.Type)
	}
	// 检测analysis监控指标
	if hasAnalysisStep(spec) {
		if spec.Analysis == nil {
			return errors.New("存在analysis步骤时必须配置spec.analysis")
		}
		if err := validateAnalysis(spec.Analysis); err != nil {
			return err
		}
		if err := validateAnalysisReferences(spec); err != nil {
			return err
		}
	}
	return nil
}

// 这个方法是检测发行步骤里不能同时setWeight和pause
func validatesteps(steps []deliveryv1alpha1.Step) error {
	lastWeight := int32(0)
	for i, step := range steps {
		count := 0
		if step.SetWeight != nil {
			count++
			if *step.SetWeight < 0 || *step.SetWeight > 100 {
				return fmt.Errorf("步骤%d的setWeight必须在0-100之间", i)
			}
			if *step.SetWeight < lastWeight {
				return fmt.Errorf("步骤%d的setwidth=%d小于前一步的%d", i, *step.SetWeight, lastWeight)
			}
			lastWeight = *step.SetWeight
		}
		if step.Pause != nil {
			count++
			duration := step.Pause.Duration.Duration
			if duration < 0 {
				return fmt.Errorf("pause.duration必须大于0")
			}
			if duration > time.Hour {
				return fmt.Errorf("pause.duration的值不能大于1小时")
			}
		}
		if step.Analysis != nil {
			count++
		}
		if count != 1 {
			return fmt.Errorf("步骤%d必须且只能设置setweight/pause/analysis之一", i)
		}
	}
	return nil
}

// 用 seen Map 查指标名是否重复
func validateAnalysis(spec *deliveryv1alpha1.AnalysisSpec) error {
	if spec.PrometheusURL == "" {
		return errors.New("spec.analysis.PrometheusURL不能为空")
	}

	if len(spec.Metrics) == 0 {
		return errors.New("Metrics不能为空")
	}

	// 失败、成功阈值布恩那个低于0
	if spec.SuccessThreshold < 0 || spec.FailureThreshold < 0 {
		return errors.New("分析阈值不能为符数")
	}

	// seen定义一个空map，接收metrics的name，判断是不是重复、query、comparison是否符合规范
	seen := map[string]bool{}
	for i, metric := range spec.Metrics {
		if metric.Name == "" {
			return fmt.Errorf("spec的metrics[%d].name不能为空", i)
		}
		if seen[metric.Name] {
			return fmt.Errorf("指标名称%q重复", metric.Name)
		}
		seen[metric.Name] = true
		if metric.Query == "" {
			return fmt.Errorf("Query不能为空")
		}
		if metric.Comparison != "" && metric.Comparison != deliveryv1alpha1.ComparisonLE && metric.Comparison != deliveryv1alpha1.ComparisonGE {
			return fmt.Errorf("%q的comparison只能是le或ge", metric.Name)
		}
	}
	return nil
}

// 对用户输入的指标名进行校验，不要不存在的
func validateAnalysisReferences(spec *deliveryv1alpha1.RolloutSpec) error {
	if spec.Strategy.Canary == nil {
		return nil
	}

	known := map[string]bool{}
	// 遍历Analysis的MetricsName名
	for _, metric := range spec.Analysis.Metrics {
		// 找到对应的MetricsName
		known[metric.Name] = true
	}

	// 查找指标
	for i, step := range spec.Strategy.Canary.Steps {
		if step.Analysis == nil {
			continue
		}
		for _, name := range step.Analysis.Metrics {
			if !known[name] {
				return fmt.Errorf("步骤%d引用了未定义的指标%q", i, name)
			}
		}
	}
	return nil
}

// 设置默认值的
func setIfMissing(parent map[string]interface{}, key string, value interface{}) {
	if _, ok := parent[key]; !ok {
		parent[key] = value
	}
}

// 如果没有这个 key，或者它不是 map，就新建一个 map 塞进去
func childMap(parent map[string]interface{}, key string) map[string]interface{} {
	if value, ok := parent[key]; ok {
		if m, ok := value.(map[string]interface{}); ok {
			return m
		}
	}
	m := map[string]interface{}{}
	parent[key] = m
	return m
}

// 判断用户传来的spec的Analysis有没有值
func hasAnalysisStep(spec *deliveryv1alpha1.RolloutSpec) bool {
	// 第一道保险：如果用户根本没配 Canary 策略（比如用的是 BlueGreen 策略），那肯定没有 Analysis 步骤。
	if spec.Strategy.Canary == nil {
		return false
	}
	// 遍历所有的发布步骤
	for _, step := range spec.Strategy.Canary.Steps {
		// 只要发现有一个步骤的 Analysis 字段不是空的，说明安排了指标分析
		if step.Analysis != nil {
			return true // 立刻返回 true
		}
	}
	// 找了一圈都没发现，返回 false
	return false
}
