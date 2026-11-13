package controller

// 测试用户暂停、继续防重试与失败证据
import (
	"strings"
	"testing"
	"time"

	deliveryv1alpha1 "github.com/kbnntg/rollout-operator/api/v1alpha1"
	"github.com/kbnntg/rollout-operator/internal/analysis"
)

// 测试用户中断更新，必须死等用户更新spec而不是一直重试
func TestRollbackBlocksUntilTemplateChanges(t *testing.T) {
	f := newFixture(t, demoRollout(
		[]deliveryv1alpha1.Step{weightStep(10), pauseStep(time.Second)},
		nil,
	))
	f.reconcile(t)
	updateImage(t, f, "nginx:1.26")
	f.reconcile(t)

	// 模拟用户终端
	setCommand(t, f, CommandAbort)
	f.reconcile(t)

	ro := f.rollout(t)
	// 现在状态是Aborted
	if ro.Status.Phase != deliveryv1alpha1.PhaseAborted {
		t.Fatalf("phase = %s, want Aborted", ro.Status.Phase)
	}
	// Hash为旧Hash
	if ro.Status.BlockedTemplateHash == "" {
		t.Fatal("blocked template hash should be recorded after rollback")
	}

	// 再次调谐不应自动重新发布统一模板
	result := f.reconcile(t)
	if result.RequeueAfter == 0 {
		t.Fatalf("blocked rollout should requeue later, got %+v", result)
	}

	ro = f.rollout(t)
	if ro.Status.Phase != deliveryv1alpha1.PhaseAborted {
		t.Fatalf("phase = %s, want Aborted while blocked", ro.Status.Phase)
	}

	// 现在用户修改模板了，可以发布了
	updateImage(t, f, "nginx:1.27")
	f.reconcile(t)
	ro = f.rollout(t)
	if ro.Status.Phase != deliveryv1alpha1.PhaseProgressing {
		t.Fatalf("phase = %s, want Progressing after template change", ro.Status.Phase)
	}
	// Blocked的Hash被清空了
	if ro.Status.BlockedTemplateHash != "" {
		t.Fatalf("blocked hash should be cleared, got %q", ro.Status.BlockedTemplateHash)
	}
}

// Prometheus指标恶化导致发布失败需要保留记录
func TestRollbackKeepsFailureEvidence(t *testing.T) {
	f := newFixture(t, demoRollout(
		[]deliveryv1alpha1.Step{weightStep(10), analysisStep()},
		analysisSpec(3, 1)))
	f.r.AnalysisClientFactory = func(string) (analysis.Client, error) {
		return fakeAnalysis{value: 0.9}, nil
	}
	f.reconcile(t)
	updateImage(t, f, "nginx:1.26")
	f.reconcile(t)

	ro := f.rollout(t)
	if ro.Status.Phase != deliveryv1alpha1.PhaseFailed {
		t.Fatalf("phase = %s, want Failed", ro.Status.Phase)
	}
	//必须停留在失败的那个步骤（即第 1 步，analysis step）
	if ro.Status.CurrentStepIndex != 1 {
		t.Fatalf("failed step index = %d, want 1 (analysis step kept for UI)", ro.Status.CurrentStepIndex)
	}
	//错误信息里必须明确指出是哪个指标挂了
	if !strings.Contains(ro.Status.Message, "error-rate") {
		t.Fatalf("rollback message should name the breached metric, got %q", ro.Status.Message)
	}
	found := false
	for _, metric := range ro.Status.Analysis {
		if metric.Name != "error-rate" {
			continue
		}
		found = true
		if metric.Failures < 1 {
			t.Fatalf("failures = %d, want >= 1", metric.Failures)
		}
		if len(metric.Samples) == 0 {
			t.Fatal("samples should be kept as failure evidence")
		}
	}
	if !found {
		t.Fatal("error-rate analysis status missing")
	}
}

// 人工暂停并且手动放行
func TestRetryCommandUnblocksSameTemplate(t *testing.T) {
	f := newFixture(t, demoRollout(
		[]deliveryv1alpha1.Step{weightStep(10), pauseStep(time.Hour)},
		nil,
	))
	f.reconcile(t)
	updateImage(t, f, "nginx:1.26")
	f.reconcile(t)

	setCommand(t, f, CommandAbort)
	f.reconcile(t)
	ro := f.rollout(t)
	if ro.Status.Phase != deliveryv1alpha1.PhaseAborted {
		t.Fatalf("phase = %s, want Aborted", ro.Status.Phase)
	}

	// 重新放行
	setCommand(t, f, CommandRetry)
	f.reconcile(t)
	ro = f.rollout(t)
	if ro.Status.Phase != deliveryv1alpha1.PhaseProgressing {
		t.Fatalf("phase = %s, want Progressing after retry", ro.Status.Phase)
	}
}
