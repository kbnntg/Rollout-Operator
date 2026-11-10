package controller

// 对控制器进行调度测试：状态步骤、创建步骤啥的
import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kbnntg/rollout-operator/api/v1alpha1"
	"github.com/kbnntg/rollout-operator/internal/analysis"
)

// fakeAnalysis实现analysis.Client
type fakeAnalysis struct {
	value float64
	err   error
}

func (f fakeAnalysis) Query(context.Context, string) (float64, error) {
	return f.value, f.err
}

type fixture struct {
	kc client.Client
	r  *RolloutReconciler
}

// 构建字典将资源放进去（Pod和Rollout的这种）
func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	// Pod、Deployment
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add clientgo scheme: %v", err)
	}
	if err := deliveryv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add delivery scheme: %v", err)
	}
	return scheme
}

// 起一个内存集群
func newFixture(t *testing.T, rollout *deliveryv1alpha1.Rollout) fixture {
	t.Helper()
	scheme := newScheme(t)
	kc := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&deliveryv1alpha1.Rollout{}). // status与主对象分开写
														WithObjects(rollout).Build() // rollout放入这个内存集群中，相当于kubectl apply
	return fixture{
		kc: kc,
		r: &RolloutReconciler{
			Client:   kc,
			Scheme:   scheme,
			Recorder: nil, //event(	内容判空，测试不要时间
		},
	}
}

// 调谐一次，在真实集群里发生唤醒Controller
func (f fixture) reconcile(t *testing.T) ctrl.Result {
	t.Helper()

	// 调用rollout_controller中的Reconcile方法
	result, err := f.r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "demo"},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return result
}

// 获取rollout、deployment、ingress数据，从假及群里，然后断言
func (f fixture) rollout(t *testing.T) *deliveryv1alpha1.Rollout {
	t.Helper()
	// 获取到的rollout数据
	ro := &deliveryv1alpha1.Rollout{}
	if err := f.kc.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "demo"}, ro); err != nil {
		t.Fatalf("get rollout: %v", err)
	}
	return ro
}

func (f fixture) deployment(t *testing.T, name string) *appsv1.Deployment {
	t.Helper()
	// 获取Deployment
	dep := &appsv1.Deployment{}
	if err := f.kc.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: name}, dep); err != nil {
		t.Fatalf("get deployment %s:%v", name, err)
	}
	return dep
}

func (f fixture) ingress(t *testing.T, name string) *networkingv1.Ingress {
	t.Helper()
	ing := &networkingv1.Ingress{}
	if err := f.kc.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: name}, ing); err != nil {
		t.Fatalf("get ingress %s:%v", name, err)
	}
	return ing
}

// 断言某个资源不存在。常用于检查“Canary部署是否被成功删除”
func (f fixture) expectNotFound(t *testing.T, obj client.Object) {
	t.Helper()
	err := f.kc.Get(context.Background(), client.ObjectKeyFromObject(obj), obj)
	if err == nil {
		t.Fatalf("%T %s should not exist", obj, client.ObjectKeyFromObject(obj).Name)
	}
}

// 创建一个测试用的Rollout
func demoRollout(steps []deliveryv1alpha1.Step, analysisSpec *deliveryv1alpha1.AnalysisSpec) *deliveryv1alpha1.Rollout {
	replicas := int32(4)
	return &deliveryv1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo",
			Namespace: "default",
			UID:       types.UID("demo-uid"),
		},
		Spec: deliveryv1alpha1.RolloutSpec{
			Replicas: &replicas,
			Port:     80,
			Ingress:  &deliveryv1alpha1.IngressSpec{Host: "demo.local", ClassName: "nginx"},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx:1.25"}}},
			},
			Strategy: deliveryv1alpha1.Strategy{
				Type: deliveryv1alpha1.StrategyCanary,
				Canary: &deliveryv1alpha1.CanaryStrategy{
					Steps:              steps,
					DynamicStableScale: true,
				},
			},
			Analysis: analysisSpec,
		},
	}
}

// 快速创建WeightStep、Pause、Analysis步骤
func weightStep(weight int32) deliveryv1alpha1.Step {
	return deliveryv1alpha1.Step{SetWeight: &weight}
}

func pauseStep(d time.Duration) deliveryv1alpha1.Step {
	return deliveryv1alpha1.Step{Pause: &deliveryv1alpha1.Pause{Duration: metav1.Duration{Duration: d}}}
}

func analysisStep() deliveryv1alpha1.Step {
	return deliveryv1alpha1.Step{Analysis: &deliveryv1alpha1.AnalysisStep{}}
}

// 创建spec的analysis的一些参数（URL、Interval、SuccessThreshold、FailureThreshol、Metrics）
func analysisSpec(success, failure int32) *deliveryv1alpha1.
	AnalysisSpec {
	return &deliveryv1alpha1.AnalysisSpec{
		PrometheusURL:    "http://prometheus:9090",
		Interval:         metav1.Duration{Duration: 5 * time.Millisecond}, // 采样间隔
		SuccessThreshold: success,
		FailureThreshold: failure,
		Metrics: []deliveryv1alpha1.AnalysisMetric{{
			// 间隔
			Name: "error-rate", Query: "sum(rate(errors[1m]))", Threshold: 0.05},
		},
	}
}

// 用户更新镜像操作
func updateImage(t *testing.T, f fixture, image string) {
	t.Helper()
	ro := f.rollout(t)
	ro.Spec.Template.Spec.Containers[0].Image = image
	if err := f.kc.Update(context.Background(), ro); err != nil {
		t.Fatalf("update rollout image: %v", err)
	}
}

// 打补丁
func setCommand(t *testing.T, f fixture, command string) {
	t.Helper()
	ro := f.rollout(t)
	if ro.Annotations == nil {
		ro.Annotations = map[string]string{}
	}
	ro.Annotations[AnnotationCommand] = command
	if err := f.kc.Update(context.Background(), ro); err != nil {
		t.Fatalf("set command %s %v", command, err)
	}
}

// 断言初始化模板
func TestInitialReconcileCreatesStableResources(t *testing.T) {
	// 初始Step值步骤weightStep值为10
	f := newFixture(t, demoRollout([]deliveryv1alpha1.Step{weightStep(10)}, nil))
	f.reconcile(t)

	ro := f.rollout(t)
	// 断言首次创建的状态肯定得是稳定的
	if ro.Status.Phase != deliveryv1alpha1.PhaseSteady {
		t.Fatalf("phase=%s, want Steady", ro.Status.Phase)
	}
	// 断言版本号1
	if ro.Status.Revision != 1 {
		t.Fatalf("revision=%d, want 1", ro.Status.Revision)
	}
	// 断言首次创建肯定有TemplateHash的，为了和后续Hash值改变知道发布版本了
	if ro.Status.StableTemplateHash == "" {
		t.Fatalf("stable template hash should be set")
	}

	// 断言deployment初始
	dep := f.deployment(t, "demo")
	// 镜像是nginx1.25
	if dep.Spec.Template.Spec.Containers[0].Image != "nginx:1.25" {
		t.Fatalf("unexpected stable image %s", dep.Spec.Template.Spec.Containers[0].Image)
	}
	// 断言Label标签是TrackStable
	if dep.Labels[LabelTrack] != TrackStable {
		t.Fatalf("stable deployment track label = %s", dep.Labels[LabelTrack])
	}
	// 断言副本数为4
	if dep.Status.Replicas != 4 {
		t.Fatalf("stable replicas = %d, want 4", *dep.Spec.Replicas)
	}
	// 断言不能有canary发布
	f.expectNotFound(t, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "demo-canary", Namespace: "default"}})

	// 断言svc创建
	svc := &corev1.Service{}
	if err := f.kc.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "demo"}, svc); err != nil {
		t.Fatalf("stable service missing: %v", err)
	}
	f.ingress(t, "demo")
}

// 模拟用户更新镜像走到第二步得策略
func TestTemplateChangeStartsCanaryWithWeightStep(t *testing.T) {
	f := newFixture(t, demoRollout(
		[]deliveryv1alpha1.Step{weightStep(10), pauseStep(time.Minute)},
		nil))
	f.reconcile(t)
	// 更新镜像
	updateImage(t, f, "nginx:1.26")
	result := f.reconcile(t)
	// 需要睡一分钟
	if result.RequeueAfter <= 0 {
		t.Fatalf("expected requeue for pause step, got %+v", result)
	}

	ro := f.rollout(t)
	// 第二步了所以肯定是发布中的步骤
	if ro.Status.Phase != deliveryv1alpha1.PhaseProgressing {
		t.Fatalf("phase = %s, want Progressing", ro.Status.Phase)
	}
	// Weight因为是发布中了，所以走到了发布10上
	if ro.Status.CurrentWeight != 10 {
		t.Fatalf("current weight = %d, want 10", ro.Status.CurrentWeight)
	}
	// 发布步骤完了第一次Weight现在是下标1，pause
	if ro.Status.CurrentStepIndex != 1 {
		t.Fatalf("current step = %d, want 1 (pause)", ro.Status.CurrentStepIndex)
	}

	// 断言发布中模板还是v1版
	stable := f.deployment(t, "demo")
	if stable.Spec.Template.Spec.Containers[0].Image != "nginx:1.25" {
		t.Fatalf("stable deployment must keep old image, got %s", stable.Spec.Template.Spec.Containers[0].Image)
	}

	// 发布中的金丝雀
	canary := f.deployment(t, "demo-canary")
	if canary.Spec.Template.Spec.Containers[0].Image != "nginx:1.26" {
		t.Fatalf("canary image = %s, want nginx:1.26", canary.Spec.Template.Spec.Containers[0].Image)
	}
	// 标签现在是Canary了
	if canary.Labels[LabelTrack] != TrackCanary {
		t.Fatalf("canary track label = %s", canary.Labels[LabelTrack])
	}
	//断言ingress权重annotation是否正确设置
	ing := f.ingress(t, "demo-canary")
	if got := ing.Annotations["nginx.ingress.kubernetes.io/canary-weight"]; got != "10" {
		t.Fatalf("canary weight annotation = %q, want 10", got)
	}
}

// 断言发布完成的结果了
func TestAnalysisPassPromotesNewRevision(t *testing.T) {
	f := newFixture(t, demoRollout(
		[]deliveryv1alpha1.Step{weightStep(10), analysisStep()},
		analysisSpec(1, 2)))
	// 注入Mock：返回0.01(小于阈值 0.05，代表成功)
	f.r.AnalysisClientFactory = func(string) (analysis.Client, error) {
		return fakeAnalysis{value: 0.01}, nil
	}
	f.reconcile(t)
	updateImage(t, f, "nginx:1.26")
	f.reconcile(t)

	ro := f.rollout(t)
	// 断言发布完成了现在状态肯定是steady了
	if ro.Status.Phase != deliveryv1alpha1.PhaseSteady {
		t.Fatalf("phase = %s, want Steady after promotion", ro.Status.Phase)
	}
	// Hash也要空了
	if ro.Status.CanaryTemplateHash != "" {
		t.Fatal("canary hash should be cleared after promotion")
	}
	// 版本号得为2了
	if ro.Status.Revision != 2 {
		t.Fatalf("revision = %d, want 2", ro.Status.Revision)
	}

	// 断言是否镜像修改了
	stable := f.deployment(t, "demo")
	if stable.Spec.Template.Spec.Containers[0].Image != "nginx:1.26" {
		t.Fatalf("stable image = %s, want nginx:1.26 after promotion", stable.Spec.Template.Spec.Containers[0].Image)
	}
	// 断言不能有canary了，因为已经发不完了
	f.expectNotFound(t, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "demo-canary", Namespace: "default"}})
	f.expectNotFound(t, &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "demo-canary", Namespace: "default"}})
}

// 断言大于阈值回滚得测试
func TestAnalysisFailureTriggersRollback(t *testing.T) {
	f := newFixture(t, demoRollout(
		[]deliveryv1alpha1.Step{weightStep(10), analysisStep()},
		analysisSpec(3, 1)))
	// 阈值超过
	f.r.AnalysisClientFactory = func(string) (analysis.Client, error) {
		return fakeAnalysis{value: 0.9}, nil
	}
	// 到了控制器中枢，发现模板改变了就进入advance中→进入advance中发现没有Analysis值调用：outcome, requeueAfter, err := r.runAnalysis(ctx, ro, step.Analysis, now)
	// cli, err := r.analysisClient(analysisSpec.PrometheusURL)直接返回0.9
	// value, qerr := cli.Query(ctx, metric.Query)之后passed := analysis.Pass(value, metric.Threshold, analysis.ComparisonOf(metric))，之后value是0.9，阈值时0.05很明显时大于阈值的entry.Failures++到阈值后phase状态就Failed了

	f.reconcile(t)
	updateImage(t, f, "nginx:1.26")
	f.reconcile(t)

	ro := f.rollout(t)
	// 判断状态是否为PhaseFailed
	if ro.Status.Phase != deliveryv1alpha1.PhaseFailed {
		t.Fatalf("phase = %s, want Failed", ro.Status.Phase)
	}
	// 回滚了看看镜像回来了吗
	stable := f.deployment(t, "demo")
	if stable.Spec.Template.Spec.Containers[0].Image != "nginx:1.25" {
		t.Fatalf("stable image = %s, must stay nginx:1.25 after rollback", stable.Spec.Template.Spec.Containers[0].Image)
	}
	// 不能是Canary发布状态了
	f.expectNotFound(t, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "demo-canary", Namespace: "default"}})
}

// 断言人工强制推送
func TestPromoteCommandPromotesImmediately(t *testing.T) {
	f := newFixture(t, demoRollout(
		[]deliveryv1alpha1.Step{weightStep(10), pauseStep(time.Hour)},
		nil))
	f.reconcile(t)
	updateImage(t, f, "nginx:1.26")
	f.reconcile(t)
	// 强推
	setCommand(t, f, CommandPromote)
	f.reconcile(t)

	ro := f.rollout(t)
	// 状态应该是Steady了
	if ro.Status.Phase != deliveryv1alpha1.PhaseSteady {
		t.Fatalf("phase = %s, want Steady", ro.Status.Phase)
	}
	// 检测Annotations有没有Command标，是否被清理
	if _, ok := ro.Annotations[AnnotationCommand]; ok {
		t.Fatal("command annotation should be cleared")
	}
	stable := f.deployment(t, "demo")
	if stable.Spec.Template.Spec.Containers[0].Image != "nginx:1.26" {
		t.Fatalf("stable image = %s, want nginx:1.26", stable.Spec.Template.Spec.Containers[0].Image)
	}
}

// 断言人工终止
func TestAbortCommandRollsBack(t *testing.T) {
	f := newFixture(t, demoRollout(
		[]deliveryv1alpha1.Step{weightStep(10), pauseStep(time.Hour)},
		nil,
	))
	f.reconcile(t)
	updateImage(t, f, "nginx:1.26")
	f.reconcile(t)
	// 人工种植
	setCommand(t, f, CommandAbort)
	f.reconcile(t)

	ro := f.rollout(t)
	// 检查状态
	if ro.Status.Phase != deliveryv1alpha1.PhaseAborted {
		t.Fatalf("phase = %s, want Aborted", ro.Status.Phase)
	}
	f.expectNotFound(t, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "demo-canary", Namespace: "default"}})
}

// 断言暂停与回复
func TestPauseAndResumeCommands(t *testing.T) {
	f := newFixture(t, demoRollout(
		[]deliveryv1alpha1.Step{weightStep(10), pauseStep(time.Hour)},
		nil,
	))
	f.reconcile(t)
	updateImage(t, f, "nginx:1.26")
	f.reconcile(t)

	// 点击暂停
	setCommand(t, f, CommandPause)
	f.reconcile(t)
	ro := f.rollout(t)
	// 停在Paused步骤
	if ro.Status.Phase != deliveryv1alpha1.PhasePaused {
		t.Fatalf("phase = %s, want Paused", ro.Status.Phase)
	}

	// 点击恢复
	setCommand(t, f, CommandResume)
	f.reconcile(t)
	ro = f.rollout(t)
	// 发布完成
	if ro.Status.Phase != deliveryv1alpha1.PhaseSteady {
		t.Fatalf("phase = %s, want Steady after resume completes remaining steps", ro.Status.Phase)
	}
}

// 删除标记
func TestFinalizerAddedAndRemoved(t *testing.T) {
	f := newFixture(t, demoRollout([]deliveryv1alpha1.Step{weightStep(10)}, nil))
	f.reconcile(t)
	ro := f.rollout(t)
	found := false
	// 遍历看看有没有Finalizaers
	for _, finalizer := range ro.Finalizers {
		if finalizer == finalizerName {
			found = true
		}
	}
	if !found {
		t.Fatalf("finalizer %q should be added, got %v", finalizerName, ro.Finalizers)
	}

	// 模拟删除+设置时间戳
	ro.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	if err := f.kc.Delete(context.Background(), ro); err != nil {
		t.Fatalf("delete rollout: %v", err)
	}
	// 再次调谐，Controller 应该识别到删除事件，清理资源并移除 Finalizer
	f.reconcile(t)
}
