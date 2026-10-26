// Package controller 实现 Rollout 控制器的调谐循环：
// 维护稳定版本 / 金丝雀版本两套 Deployment、Service、Ingress，
// 按步骤推进流量权重，结合 Prometheus 指标自动判定发布结果并回滚。
package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	deliveryv1alpha1 "github.com/kbnntg/rollout-operator/api/v1alpha1"
	"github.com/kbnntg/rollout-operator/internal/metrics"
)

const (
	// finalizerName 保证删除 Rollout 时不会留下孤立资源。
	finalizerName = "delivery.sre.dev/finalizer"

	// LabelApp 应用名标签，用于跨版本选择 Pod。
	LabelApp = "app.kubernetes.io/name"
	// LabelRollout 归属 Rollout 标签。
	LabelRollout = "delivery.sre.dev/rollout"
	// LabelTrack 版本轨道（stable / canary）。
	LabelTrack = "delivery.sre.dev/track"

	// TrackStable 稳定版本轨道。
	TrackStable = "stable"
	// TrackCanary 金丝雀版本轨道。
	TrackCanary = "canary"

	// AnnotationCommand 控制台向 Operator 下发命令的注解键。
	AnnotationCommand = "delivery.sre.dev/command"
	// CommandPromote 立即全量发布。
	CommandPromote = "promote"
	// CommandAbort 立即回滚。
	CommandAbort = "abort"
	// CommandPause 暂停发布流程。
	CommandPause = "pause"
	// CommandResume 恢复发布流程。
	CommandResume = "resume"
	// CommandRetry 解除失败/中止阻塞，重新发起同一版本发布。
	CommandRetry = "retry"

	// ConditionTypeProgressing 发布进行中。
	ConditionTypeProgressing = "Progressing"
	// ConditionTypeReady 稳定版本已就绪。
	ConditionTypeReady = "Ready"
	// ConditionTypeAnalysis 分析状态。
	ConditionTypeAnalysis = "Analysis"
)

// RolloutReconciler 调谐 Rollout 对象。
type RolloutReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=delivery.sre.dev,resources=rollouts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.sre.dev,resources=rollouts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.sre.dev,resources=rollouts/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete

// Reconcile 是 Rollout 的调谐入口。
func (r *RolloutReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ro := &deliveryv1alpha1.Rollout{}
	if err := r.Get(ctx, req.NamespacedName, ro); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !ro.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.handleDelete(ctx, ro)
	}

	if controllerutil.AddFinalizer(ro, finalizerName) {
		if err := r.Update(ctx, ro); err != nil {
			return ctrl.Result{}, err
		}
	}
	original := ro.DeepCopy()

	// 补默认值（只在内存里，API Server 端的 CRD 默认值才真正落盘）
	normalize(ro)

	// 处理前端/用户下发的命令注解（promote/abort/pause/resume/retry）
	if _, err := r.applyCommand(ctx, ro); err != nil {
		return ctrl.Result{}, err
	}

	res, reconcileErr := r.reconcileRollout(ctx, ro)
	if reconcileErr != nil {
		r.setCondition(&ro.Status.Conditions, ConditionTypeReady, metav1.ConditionFalse, "ReconcileError", reconcileErr.Error(), ro.Generation)
	}

	// status 有变化才写回，避免写放大和事件风暴
	if !apiequality.Semantic.DeepEqual(original.Status, ro.Status) {
		ro.Status.ObservedGeneration = ro.Generation
		if err := r.Status().Update(ctx, ro); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}
	return res, reconcileErr
}

// SetupWithManager 注册控制器及其监听资源。
func (r *RolloutReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.Rollout{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.Ingress{}).
		Complete(r)
}

// ---------------------------------------------------------------------------
// 主流程
// ---------------------------------------------------------------------------

func (r *RolloutReconciler) reconcileRollout(ctx context.Context, ro *deliveryv1alpha1.Rollout) (ctrl.Result, error) {
	spec := &ro.Spec
	st := &ro.Status
	now := time.Now()
	replicas := replicasOf(spec)

	// 首次创建：只创建一个稳定版本。
	if st.Phase == "" && st.StableTemplateHash == "" {
		if err := r.ensureDeployment(ctx, ro, ro.Name, TrackStable, &spec.Template, replicas); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.ensureService(ctx, ro, ro.Name, TrackStable); err != nil {
			return ctrl.Result{}, err
		}
		if spec.Ingress != nil {
			if err := r.ensurePrimaryIngress(ctx, ro); err != nil {
				return ctrl.Result{}, err
			}
		}
		st.StableTemplateHash = hashTemplate(&spec.Template)
		st.Revision = 1
		st.Phase = deliveryv1alpha1.PhaseSteady
		st.CurrentStepIndex = -1
		st.Message = "initialized with stable revision 1"
		r.setCondition(&st.Conditions, ConditionTypeReady, metav1.ConditionTrue, "Initialized", "stable revision is serving traffic", ro.Generation)
		r.setCondition(&st.Conditions, ConditionTypeProgressing, metav1.ConditionFalse, "Steady", "no rollout in progress", ro.Generation)
		r.event(ro, corev1.EventTypeNormal, "Initialized", "created stable revision 1")
		metrics.CanaryWeight.WithLabelValues(ro.Namespace, ro.Name).Set(0)
		return ctrl.Result{}, nil
	}

	newHash := hashTemplate(&spec.Template)
	canaryActive := st.CanaryTemplateHash != ""

	// 上一次发布失败/中止，且 spec 未再变更：保持阻塞，避免反复自动重试坏版本。
	if !canaryActive && newHash != st.StableTemplateHash && newHash == st.BlockedTemplateHash {
		if phase := st.Phase; phase != deliveryv1alpha1.PhaseFailed && phase != deliveryv1alpha1.PhaseAborted {
			st.Phase = deliveryv1alpha1.PhaseFailed
		}
		if st.Message == "" {
			st.Message = "previous rollout failed; update the pod template or send retry command to unblock"
		}
		blockedTemplate, err := r.blockedStableTemplate(ctx, ro)
		if err != nil {
			return ctrl.Result{}, err
		}
		if err := r.ensureDeployment(ctx, ro, ro.Name, TrackStable, blockedTemplate, replicas); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.ensureService(ctx, ro, ro.Name, TrackStable); err != nil {
			return ctrl.Result{}, err
		}
		if spec.Ingress != nil {
			if err := r.ensurePrimaryIngress(ctx, ro); err != nil {
				return ctrl.Result{}, err
			}
		}
		r.setCondition(&st.Conditions, ConditionTypeProgressing, metav1.ConditionFalse, "Blocked",
			"rollout is blocked by a previous failure; update the pod template or send retry command to unblock", ro.Generation)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// 发现 Pod 模板变化：开启一轮金丝雀发布。
	if !canaryActive && newHash != st.StableTemplateHash {
		st.CanaryTemplateHash = newHash
		st.BlockedTemplateHash = ""
		st.Phase = deliveryv1alpha1.PhaseProgressing
		st.CurrentWeight = 0
		beginStep(st, 0, now)
		st.Message = fmt.Sprintf("rollout started, target revision %d", st.Revision+1)
		r.event(ro, corev1.EventTypeNormal, "RolloutStarted", "detected pod template change, starting canary")
		metrics.RolloutsStarted.WithLabelValues(ro.Namespace).Inc()
		canaryActive = true
	}

	// 模板回退：用户把 spec 改回线上版本，取消当前发布。
	if canaryActive && newHash == st.StableTemplateHash {
		canaryActive = false
		st.BlockedTemplateHash = ""
		if err := r.deleteCanaryResources(ctx, ro); err != nil {
			return ctrl.Result{}, err
		}
		st.CanaryTemplateHash = ""
		st.CurrentStepIndex = -1
		st.CurrentWeight = 0
		st.StepStartedAt = nil
		st.LastAnalysisAt = nil
		st.Phase = deliveryv1alpha1.PhaseSteady
		st.Message = "rollout canceled: template reverted to stable revision"
		r.event(ro, corev1.EventTypeNormal, "RolloutCanceled", "template reverted to stable revision")
		metrics.CanaryWeight.WithLabelValues(ro.Namespace, ro.Name).Set(0)
	}

	// 稳定版本：发布期间保持旧模板，只调整副本数。
	stableTemplate, err := r.stableTemplate(ctx, ro, canaryActive)
	if err != nil {
		return ctrl.Result{}, err
	}
	stableReplicas := replicas
	if canaryActive && dynamicStableScale(spec) {
		stableReplicas = scaledReplicas(replicas, 100-st.CurrentWeight)
	}
	if err := r.ensureDeployment(ctx, ro, ro.Name, TrackStable, stableTemplate, stableReplicas); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureService(ctx, ro, ro.Name, TrackStable); err != nil {
		return ctrl.Result{}, err
	}
	if spec.Ingress != nil {
		if err := r.ensurePrimaryIngress(ctx, ro); err != nil {
			return ctrl.Result{}, err
		}
	}

	if canaryActive {
		canaryRep := replicas
		if dynamicStableScale(spec) {
			canaryRep = scaledReplicas(replicas, st.CurrentWeight)
		}
		if err := r.ensureDeployment(ctx, ro, canaryName(ro), TrackCanary, &spec.Template, canaryRep); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.ensureService(ctx, ro, canaryName(ro), TrackCanary); err != nil {
			return ctrl.Result{}, err
		}
		if spec.Ingress != nil {
			if err := r.ensureCanaryIngress(ctx, ro, st.CurrentWeight); err != nil {
				return ctrl.Result{}, err
			}
		}
		return r.advance(ctx, ro)
	}

	st.Phase = deliveryv1alpha1.PhaseSteady
	r.setCondition(&st.Conditions, ConditionTypeProgressing, metav1.ConditionFalse, "Steady", "no rollout in progress", ro.Generation)
	r.setCondition(&st.Conditions, ConditionTypeReady, metav1.ConditionTrue, "Steady", "stable revision is serving traffic", ro.Generation)
	return ctrl.Result{}, nil
}

// advance 推进发布步骤：setWeight → pause → analysis → … → promote。
func (r *RolloutReconciler) advance(ctx context.Context, ro *deliveryv1alpha1.Rollout) (ctrl.Result, error) {
	spec := &ro.Spec
	st := &ro.Status
	steps := canarySteps(spec)
	now := time.Now()

	if st.Phase == deliveryv1alpha1.PhasePaused {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	if st.CurrentStepIndex < 0 {
		beginStep(st, 0, now)
	}
	if len(steps) == 0 {
		return ctrl.Result{}, r.promote(ctx, ro)
	}

	for i := 0; i < len(steps)+2 && st.CurrentStepIndex < int32(len(steps)); i++ {
		step := steps[st.CurrentStepIndex]
		switch {
		case step.SetWeight != nil:
			weight := clampWeight(*step.SetWeight)
			st.CurrentWeight = weight
			if spec.Ingress != nil {
				if err := r.ensureCanaryIngress(ctx, ro, weight); err != nil {
					return ctrl.Result{}, err
				}
			}
			if err := r.scaleCanary(ctx, ro, weight); err != nil {
				return ctrl.Result{}, err
			}
			metrics.CanaryWeight.WithLabelValues(ro.Namespace, ro.Name).Set(float64(weight))
			r.setCondition(&st.Conditions, ConditionTypeProgressing, metav1.ConditionTrue, "StepSetWeight",
				fmt.Sprintf("canary traffic weight set to %d%%", weight), ro.Generation)
			beginStep(st, st.CurrentStepIndex+1, now)

		case step.Pause != nil:
			duration := step.Pause.Duration.Duration
			if st.StepStartedAt == nil {
				st.StepStartedAt = &metav1.Time{Time: now}
			}
			elapsed := now.Sub(st.StepStartedAt.Time)
			if duration <= 0 || elapsed >= duration {
				beginStep(st, st.CurrentStepIndex+1, now)
				continue
			}
			r.setCondition(&st.Conditions, ConditionTypeProgressing, metav1.ConditionTrue, "StepPaused",
				fmt.Sprintf("waiting %s before next step", (duration-elapsed).Round(time.Second)), ro.Generation)
			return ctrl.Result{RequeueAfter: duration - elapsed}, nil

		case step.Analysis != nil:
			// ---- 第 6 节在这里接入 Prometheus 指标门禁 ----
			// 真正实现：采样 PromQL → 连续达标放行 / 连续失败自动回滚。
			// 本节先无条件放行，先跑通步骤推进 → promote 的完整链路。
			r.setCondition(&st.Conditions, ConditionTypeAnalysis, metav1.ConditionTrue, "AnalysisSkipped", "analysis gate is implemented in section 6", ro.Generation)
			beginStep(st, st.CurrentStepIndex+1, now)

		default:
			beginStep(st, st.CurrentStepIndex+1, now)
		}
	}

	return ctrl.Result{}, r.promote(ctx, ro)
}

// promote 将金丝雀模板提升为稳定版本。
func (r *RolloutReconciler) promote(ctx context.Context, ro *deliveryv1alpha1.Rollout) error {
	spec := &ro.Spec
	st := &ro.Status

	if err := r.ensureDeployment(ctx, ro, ro.Name, TrackStable, &spec.Template, replicasOf(spec)); err != nil {
		return err
	}
	if err := r.deleteCanaryResources(ctx, ro); err != nil {
		return err
	}

	st.StableTemplateHash = hashTemplate(&spec.Template)
	st.CanaryTemplateHash = ""
	st.BlockedTemplateHash = ""
	st.CurrentStepIndex = -1
	st.CurrentWeight = 0
	st.StepStartedAt = nil
	st.Revision++
	st.Phase = deliveryv1alpha1.PhaseSteady
	st.Message = fmt.Sprintf("promoted to revision %d", st.Revision)
	resetAnalysis(st)
	r.setCondition(&st.Conditions, ConditionTypeProgressing, metav1.ConditionFalse, "Promoted", "canary promoted to stable", ro.Generation)
	r.setCondition(&st.Conditions, ConditionTypeReady, metav1.ConditionTrue, "Promoted", "stable revision is serving traffic", ro.Generation)
	r.event(ro, corev1.EventTypeNormal, "Promoted", fmt.Sprintf("revision %d is now stable", st.Revision))
	metrics.RolloutsPromoted.WithLabelValues(ro.Namespace).Inc()
	metrics.CanaryWeight.WithLabelValues(ro.Namespace, ro.Name).Set(0)
	return nil
}

// rollback 终止发布并清理金丝雀资源，稳定版本继续承载流量。
func (r *RolloutReconciler) rollback(ctx context.Context, ro *deliveryv1alpha1.Rollout, phase deliveryv1alpha1.RolloutPhase, message, reason string) error {
	spec := &ro.Spec
	st := &ro.Status

	if err := r.deleteCanaryResources(ctx, ro); err != nil {
		return err
	}

	// 恢复稳定版本副本数；若稳定版本 Deployment 已被删除，则用当前 spec 重建。
	var stableTemplate *corev1.PodTemplateSpec
	dep := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ro.Namespace, Name: ro.Name}, dep); err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		stableTemplate = &spec.Template
	}
	if err := r.ensureDeployment(ctx, ro, ro.Name, TrackStable, stableTemplate, replicasOf(spec)); err != nil {
		return err
	}

	st.CanaryTemplateHash = ""
	st.BlockedTemplateHash = hashTemplate(&spec.Template)
	st.CurrentWeight = 0
	st.StepStartedAt = nil
	st.Phase = phase
	st.Message = message
	r.setCondition(&st.Conditions, ConditionTypeProgressing, metav1.ConditionFalse, "RolledBack", message, ro.Generation)
	r.setCondition(&st.Conditions, ConditionTypeReady, metav1.ConditionTrue, "RolledBack", "stable revision is serving traffic", ro.Generation)
	r.event(ro, corev1.EventTypeWarning, "RolledBack", message)
	metrics.RolloutsRolledBack.WithLabelValues(ro.Namespace, reason).Inc()
	metrics.CanaryWeight.WithLabelValues(ro.Namespace, ro.Name).Set(0)
	return nil
}

func (r *RolloutReconciler) handleDelete(ctx context.Context, ro *deliveryv1alpha1.Rollout) error {
	if controllerutil.RemoveFinalizer(ro, finalizerName) {
		return r.Update(ctx, ro)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 命令处理
// ---------------------------------------------------------------------------

func (r *RolloutReconciler) applyCommand(ctx context.Context, ro *deliveryv1alpha1.Rollout) (bool, error) {
	command, ok := ro.Annotations[AnnotationCommand]
	if !ok || command == "" {
		return false, nil
	}

	st := &ro.Status
	switch command {
	case CommandPromote:
		if st.CanaryTemplateHash != "" {
			st.Phase = deliveryv1alpha1.PhaseProgressing
			st.CurrentStepIndex = int32(len(canarySteps(&ro.Spec)))
			st.StepStartedAt = &metav1.Time{Time: time.Now()}
			st.Message = "manual promote requested"
			r.event(ro, corev1.EventTypeNormal, "Promote", "manual promote requested")
		}
	case CommandPause:
		if st.CanaryTemplateHash != "" && st.Phase == deliveryv1alpha1.PhaseProgressing {
			st.Phase = deliveryv1alpha1.PhasePaused
			st.Message = "paused by user"
			r.event(ro, corev1.EventTypeNormal, "Pause", "rollout paused by user")
		}
	case CommandResume:
		if st.Phase == deliveryv1alpha1.PhasePaused {
			st.Phase = deliveryv1alpha1.PhaseProgressing
			st.Message = "resumed by user"
			if st.StepStartedAt != nil && st.CurrentStepIndex >= 0 {
				if step := stepAt(&ro.Spec, st.CurrentStepIndex); step != nil && step.Pause != nil {
					// 跳过剩余等待时间。
					st.StepStartedAt = &metav1.Time{Time: time.Now().Add(-step.Pause.Duration.Duration)}
				}
			}
			r.event(ro, corev1.EventTypeNormal, "Resume", "rollout resumed by user")
		}
	case CommandAbort:
		if st.CanaryTemplateHash != "" {
			if err := r.rollback(ctx, ro, deliveryv1alpha1.PhaseAborted, "aborted by user", "user_abort"); err != nil {
				return false, err
			}
		}
	case CommandRetry:
		if st.CanaryTemplateHash == "" && st.BlockedTemplateHash != "" {
			st.BlockedTemplateHash = ""
			st.Phase = deliveryv1alpha1.PhaseProgressing
			st.Message = "retry requested, restarting rollout"
			r.event(ro, corev1.EventTypeNormal, "Retry", "retry requested by user")
		}
	default:
		r.event(ro, corev1.EventTypeWarning, "UnknownCommand", fmt.Sprintf("unknown command %q", command))
	}

	return true, r.clearCommand(ctx, ro)
}

func (r *RolloutReconciler) clearCommand(ctx context.Context, ro *deliveryv1alpha1.Rollout) error {
	if _, ok := ro.Annotations[AnnotationCommand]; !ok {
		return nil
	}
	// Patch 会返回服务端对象并覆盖本地 Status，导致本周期内做好的状态变更丢失；
	// 这里先保存状态，Patch 后再恢复。
	status := ro.Status.DeepCopy()
	patch := client.RawPatch(client.MergePatchType, []byte(fmt.Sprintf(
		`{"metadata":{"annotations":{%q:null}}}`, AnnotationCommand,
	)))
	if err := r.Patch(ctx, ro, patch); err != nil {
		return err
	}
	ro.Status = *status
	return nil
}

// ---------------------------------------------------------------------------
// 资源维护
// ---------------------------------------------------------------------------

func (r *RolloutReconciler) stableTemplate(ctx context.Context, ro *deliveryv1alpha1.Rollout, canaryActive bool) (*corev1.PodTemplateSpec, error) {
	if !canaryActive {
		return &ro.Spec.Template, nil
	}
	// 发布期间沿用既有稳定版本 Deployment 的模板，避免把新版本提前放量。
	dep := &appsv1.Deployment{}
	err := r.Get(ctx, client.ObjectKey{Namespace: ro.Namespace, Name: ro.Name}, dep)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return &ro.Spec.Template, nil
		}
		return nil, err
	}
	return &dep.Spec.Template, nil
}

// blockedStableTemplate 在发布被阻塞时保持现有稳定版本模板；
// 若稳定版本 Deployment 已经被删除，则使用当前 spec 重建。
func (r *RolloutReconciler) blockedStableTemplate(ctx context.Context, ro *deliveryv1alpha1.Rollout) (*corev1.PodTemplateSpec, error) {
	dep := &appsv1.Deployment{}
	err := r.Get(ctx, client.ObjectKey{Namespace: ro.Namespace, Name: ro.Name}, dep)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return &ro.Spec.Template, nil
		}
		return nil, err
	}
	return nil, nil
}

func (r *RolloutReconciler) ensureDeployment(ctx context.Context, ro *deliveryv1alpha1.Rollout, name, track string, template *corev1.PodTemplateSpec, replicas int32) error {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ro.Namespace}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		if dep.Labels == nil {
			dep.Labels = map[string]string{}
		}
		dep.Labels[LabelApp] = ro.Name
		dep.Labels[LabelRollout] = ro.Name
		dep.Labels[LabelTrack] = track

		dep.Spec.Replicas = int32Ptr(replicas)
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{
			LabelApp:   ro.Name,
			LabelTrack: track,
		}}
		if template != nil {
			dep.Spec.Template = podTemplateWithLabels(ro, *template, track)
		}
		if dep.Spec.Template.Labels == nil {
			dep.Spec.Template.Labels = map[string]string{}
		}
		dep.Spec.Template.Labels[LabelApp] = ro.Name
		dep.Spec.Template.Labels[LabelRollout] = ro.Name
		dep.Spec.Template.Labels[LabelTrack] = track
		return controllerutil.SetControllerReference(ro, dep, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("ensure deployment %s/%s: %w", ro.Namespace, name, err)
	}
	return nil
}

func (r *RolloutReconciler) ensureService(ctx context.Context, ro *deliveryv1alpha1.Rollout, name, track string) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ro.Namespace}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if svc.Labels == nil {
			svc.Labels = map[string]string{}
		}
		svc.Labels[LabelApp] = ro.Name
		svc.Labels[LabelRollout] = ro.Name
		svc.Labels[LabelTrack] = track

		if svc.Spec.Selector == nil {
			svc.Spec.Selector = map[string]string{}
		}
		svc.Spec.Selector[LabelApp] = ro.Name
		svc.Spec.Selector[LabelTrack] = track

		port := ro.Spec.Port
		if len(svc.Spec.Ports) == 0 {
			svc.Spec.Ports = []corev1.ServicePort{{
				Name:       "http",
				Protocol:   corev1.ProtocolTCP,
				Port:       port,
				TargetPort: intstr.FromInt32(port),
			}}
		} else if svc.Spec.Ports[0].Port != port {
			svc.Spec.Ports[0].Port = port
			svc.Spec.Ports[0].TargetPort = intstr.FromInt32(port)
		}
		return controllerutil.SetControllerReference(ro, svc, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("ensure service %s/%s: %w", ro.Namespace, name, err)
	}
	return nil
}

func (r *RolloutReconciler) ensurePrimaryIngress(ctx context.Context, ro *deliveryv1alpha1.Rollout) error {
	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: ro.Name, Namespace: ro.Namespace}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ing, func() error {
		if ing.Labels == nil {
			ing.Labels = map[string]string{}
		}
		ing.Labels[LabelApp] = ro.Name
		ing.Labels[LabelRollout] = ro.Name

		ing.Spec.IngressClassName = ingressClassName(ro)
		ing.Spec.Rules = ingressRules(ro, ro.Name)
		return controllerutil.SetControllerReference(ro, ing, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("ensure ingress %s/%s: %w", ro.Namespace, ro.Name, err)
	}
	return nil
}

func (r *RolloutReconciler) ensureCanaryIngress(ctx context.Context, ro *deliveryv1alpha1.Rollout, weight int32) error {
	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: canaryName(ro), Namespace: ro.Namespace}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ing, func() error {
		if ing.Labels == nil {
			ing.Labels = map[string]string{}
		}
		ing.Labels[LabelApp] = ro.Name
		ing.Labels[LabelRollout] = ro.Name
		ing.Labels[LabelTrack] = TrackCanary

		if ing.Annotations == nil {
			ing.Annotations = map[string]string{}
		}
		ing.Annotations["nginx.ingress.kubernetes.io/canary"] = "true"
		ing.Annotations["nginx.ingress.kubernetes.io/canary-weight"] = strconv.Itoa(int(clampWeight(weight)))

		ing.Spec.IngressClassName = ingressClassName(ro)
		ing.Spec.Rules = ingressRules(ro, canaryName(ro))
		return controllerutil.SetControllerReference(ro, ing, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("ensure canary ingress %s/%s: %w", ro.Namespace, canaryName(ro), err)
	}
	return nil
}

func (r *RolloutReconciler) scaleCanary(ctx context.Context, ro *deliveryv1alpha1.Rollout, weight int32) error {
	replicas := replicasOf(&ro.Spec)
	if dynamicStableScale(&ro.Spec) {
		replicas = scaledReplicas(replicas, weight)
	}
	return r.ensureDeployment(ctx, ro, canaryName(ro), TrackCanary, &ro.Spec.Template, replicas)
}

func (r *RolloutReconciler) deleteCanaryResources(ctx context.Context, ro *deliveryv1alpha1.Rollout) error {
	targets := []client.Object{
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: canaryName(ro), Namespace: ro.Namespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: canaryName(ro), Namespace: ro.Namespace}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: canaryName(ro), Namespace: ro.Namespace}},
	}
	for _, obj := range targets {
		if err := r.Delete(ctx, obj); err != nil && !k8serrors.IsNotFound(err) {
			return fmt.Errorf("delete canary resource %s/%s: %w", ro.Namespace, obj.GetName(), err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 状态辅助
// ---------------------------------------------------------------------------

func (r *RolloutReconciler) setCondition(conditions *[]metav1.Condition, conditionType string, status metav1.ConditionStatus, reason, message string, generation int64) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: generation,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})
}

func (r *RolloutReconciler) event(ro *deliveryv1alpha1.Rollout, eventType, reason, message string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Event(ro, eventType, reason, message)
}

// beginStep 进入指定步骤并重置计时器与分析计数。
func beginStep(st *deliveryv1alpha1.RolloutStatus, index int32, now time.Time) {
	st.CurrentStepIndex = index
	st.StepStartedAt = &metav1.Time{Time: now}
	st.LastAnalysisAt = nil
	resetAnalysis(st)
}

func resetAnalysis(st *deliveryv1alpha1.RolloutStatus) {
	for i := range st.Analysis {
		st.Analysis[i].Successes = 0
		st.Analysis[i].Failures = 0
	}
}

// ---------------------------------------------------------------------------
// 纯函数工具
// ---------------------------------------------------------------------------

func normalize(ro *deliveryv1alpha1.Rollout) {
	spec := &ro.Spec
	if spec.Replicas == nil {
		spec.Replicas = int32Ptr(1)
	}
	if spec.Port == 0 {
		spec.Port = 80
	}
	if spec.Ingress != nil && spec.Ingress.Path == "" {
		spec.Ingress.Path = "/"
	}
	if spec.Strategy.Type == "" {
		spec.Strategy.Type = deliveryv1alpha1.StrategyCanary
	}
	if spec.Analysis != nil {
		if spec.Analysis.Provider == "" {
			spec.Analysis.Provider = "prometheus"
		}
		if spec.Analysis.Interval.Duration <= 0 {
			spec.Analysis.Interval = metav1.Duration{Duration: 10 * time.Second}
		}
		if spec.Analysis.SuccessThreshold <= 0 {
			spec.Analysis.SuccessThreshold = 3
		}
		if spec.Analysis.FailureThreshold <= 0 {
			spec.Analysis.FailureThreshold = 2
		}
	}
}

func hashTemplate(template *corev1.PodTemplateSpec) string {
	data, err := json.Marshal(template)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func replicasOf(spec *deliveryv1alpha1.RolloutSpec) int32 {
	if spec.Replicas == nil {
		return 1
	}
	return *spec.Replicas
}

func canarySteps(spec *deliveryv1alpha1.RolloutSpec) []deliveryv1alpha1.Step {
	if spec.Strategy.Canary == nil {
		return nil
	}
	return spec.Strategy.Canary.Steps
}

func dynamicStableScale(spec *deliveryv1alpha1.RolloutSpec) bool {
	return spec.Strategy.Canary != nil && spec.Strategy.Canary.DynamicStableScale
}

func stepAt(spec *deliveryv1alpha1.RolloutSpec, index int32) *deliveryv1alpha1.Step {
	steps := canarySteps(spec)
	if index < 0 || int(index) >= len(steps) {
		return nil
	}
	return &steps[index]
}

func clampWeight(weight int32) int32 {
	if weight < 0 {
		return 0
	}
	if weight > 100 {
		return 100
	}
	return weight
}

func scaledReplicas(total, percent int32) int32 {
	if total <= 0 || percent <= 0 {
		return 0
	}
	if percent >= 100 {
		return total
	}
	scaled := int32(math.Ceil(float64(total) * float64(percent) / 100.0))
	if scaled < 1 {
		scaled = 1
	}
	if scaled > total {
		scaled = total
	}
	return scaled
}

func canaryName(ro *deliveryv1alpha1.Rollout) string {
	return ro.Name + "-canary"
}

func ingressClassName(ro *deliveryv1alpha1.Rollout) *string {
	if ro.Spec.Ingress == nil || ro.Spec.Ingress.ClassName == "" {
		return nil
	}
	name := ro.Spec.Ingress.ClassName
	return &name
}

func ingressRules(ro *deliveryv1alpha1.Rollout, serviceName string) []networkingv1.IngressRule {
	path := "/"
	host := ""
	if ro.Spec.Ingress != nil {
		if ro.Spec.Ingress.Path != "" {
			path = ro.Spec.Ingress.Path
		}
		host = ro.Spec.Ingress.Host
	}
	pathType := networkingv1.PathTypePrefix
	rule := networkingv1.IngressRule{
		IngressRuleValue: networkingv1.IngressRuleValue{
			HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{
					Path:     path,
					PathType: &pathType,
					Backend: networkingv1.IngressBackend{
						Service: &networkingv1.IngressServiceBackend{
							Name: serviceName,
							Port: networkingv1.ServiceBackendPort{Number: ro.Spec.Port},
						},
					},
				}},
			},
		},
	}
	if host != "" {
		rule.Host = host
	}
	return []networkingv1.IngressRule{rule}
}

func podTemplateWithLabels(ro *deliveryv1alpha1.Rollout, template corev1.PodTemplateSpec, track string) corev1.PodTemplateSpec {
	out := *template.DeepCopy()
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	out.Labels[LabelApp] = ro.Name
	out.Labels[LabelRollout] = ro.Name
	out.Labels[LabelTrack] = track
	return out
}

func int32Ptr(v int32) *int32 {
	return &v
}
