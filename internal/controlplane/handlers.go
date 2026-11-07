package controlplane

// 与前端交互的模块、将Types、auth、audit、metrics、server这些操作都一起使用处理前端请求
import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kbnntg/rollout-operator/api/v1alpha1"
	"github.com/kbnntg/rollout-operator/internal/analysis"
	rolloutcontroller "github.com/kbnntg/rollout-operator/internal/controller"
)

// 获取登陆账号密码并对其进行校验
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	// 这是防止DDOS攻击，设置最大请求1M
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		s.withError(w, http.StatusBadRequest, "请求体格式错误")
		return
	}

	if req.Username == "" || req.Password == "" {
		s.withError(w, http.StatusBadRequest, "账号密码为空")
		return
	}
	// 将账号密码放入到Auth.Authenticate方法中获取身份
	role, ok := s.Auth.Authenticate(req.Username, req.Password)
	if !ok {
		s.withError(w, http.StatusUnauthorized, "用户或密码错误")
		return
	}

	// 获取token值，通过s.Auth.Issue方法（将用户名和身份进行验证）
	token, expiresAt, err := s.Auth.Issue(req.Username, role)
	if err != nil {
		s.withError(w, http.StatusInternalServerError, "令牌签发失败")
		return
	}

	// 一切操作都没问题就写入Json，给前端交互
	s.writeJson(w, http.StatusOK, LoginResponse{
		Token:    token,
		User:     UserInfo{Name: req.Username, Role: role},
		ExpireAt: nowStamp(expiresAt),
	})
}

// 获取认证信息，从claimsFrom方法中获取
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	if claims == nil {
		s.withError(w, http.StatusUnauthorized, "没认证")
		return
	}
	s.writeJson(w, http.StatusOK, UserInfo{Name: claims.Subject, Role: claims.Role})
}

// 获取Rollout列查发布列表，可以用namespace过滤
func (s *Server) handleListRollouts(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	options := []client.ListOption{}
	if namespace != "" {
		options = append(options, client.InNamespace(namespace))
	}

	// 获取列表
	var list deliveryv1alpha1.RolloutList
	if err := s.Client.List(r.Context(), &list, options...); err != nil {
		s.Logger.Error(err, "list rollouts failed")
		s.withError(w, http.StatusInternalServerError, "查询Rollout失败")
		return
	}

	// 遍历列表，调用：buildSummary转换
	items := make([]RolloutSummary, 0, len(list.Items))
	for i := range list.Items {
		items = append(items, s.buildSummary(&list.Items[i]))
	}

	// 时间排序，获取最新
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt > items[j].CreatedAt
	})

	s.writeJson(w, http.StatusOK, map[string]interface{}{"items": items})
}

// 前端发来参数，查找对应的相关的Pod和Deployment之后组装成Json
func (s *Server) handleRolloutDetail(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")

	// 获取rollout列表
	ro := &deliveryv1alpha1.Rollout{}
	if err := s.Client.Get(r.Context(), client.ObjectKey{Namespace: namespace, Name: name}, ro); err != nil {
		if apierrors.IsNotFound(err) {
			s.withError(w, http.StatusNotFound, "Rollout不存在")
			return
		}
		s.Logger.Error(err, "get rollout failed", "namespace", namespace, "name", name)
		s.withError(w, http.StatusInternalServerError, "查询ROllout失败")
		return
	}
	s.writeJson(w, http.StatusOK, s.buildDetail(r.Context(), ro))
}

// 前端发来动作，执行打补丁（Patch）之后写入审计日志
func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")

	// 请求，对应的之前type.go里创建的ActionRequest		Action string `json:"action"`
	var req ActionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		s.withError(w, http.StatusBadRequest, "别攻击了")
		return
	}

	// 判断这个动作是否存在
	if !validAction(req.Action) {
		s.withError(w, http.StatusBadRequest, "不支持这个动作，仅支持promote/abort/pause/resume")
		return
	}

	// 获取Rollout表
	ro := &deliveryv1alpha1.Rollout{}
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: namespace}, ro); err != nil {
		if apierrors.IsNotFound(err) {
			s.withError(w, http.StatusNotFound, "Rollout不存在")
			return
		}
		s.withError(w, http.StatusInternalServerError, "查询Rollout失败")
		return
	}

	// 修改
	patch := client.RawPatch(types.MergePatchType, []byte(fmt.Sprintf(
		`{"metadata":{"annotations":{%q:%q}}}`, rolloutcontroller.AnnotationCommand, req.Action,
	)))
	result := "accepted"
	message := "命令一下放，operator再下一个周期执行"
	if err := s.Client.Patch(r.Context(), ro, patch); err != nil {
		s.Logger.Error(err, "patch rollout failed", "rollout", name)
		result = "failed"
		message = "命令下方失败"
	}
	// 写入审计的
	record := AuditRecord{
		ID:        randomID(),
		Time:      nowStamp(time.Now()),
		User:      claims.Subject,
		Role:      claims.Role,
		Action:    req.Action,
		Namespace: namespace,
		Rollout:   name,
		Detail:    "dispatched from rollout console",
		Result:    result,
		RemoteIP:  r.RemoteAddr,
	}
	// 写入审计中，调用Audit的Append方法
	if err := s.Audit.Append(r.Context(), record); err != nil {
		s.Logger.Error(err, "append audit record failed")
	} else {
		s.Metrics.recordAudit()
	}
	s.Metrics.recordAction(req.Action, claims.Role, result)

	// 如果状态不是接受，就报错
	if result != "accepted" {
		s.withError(w, http.StatusInternalServerError, message)
		return
	}
	s.writeJson(w, http.StatusAccepted, ActionResponse{OK: true, Message: message})
}

// 日志查询
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		// 0到1000之间
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed < 1000 {
			limit = parsed
		}
	}
	// 查询日志
	records, err := s.Audit.List(r.Context(), namespace, limit)
	if err != nil {
		s.Logger.Error(err, "limit audit records failed")
		s.withError(w, http.StatusInternalServerError, "查询日志失败")
		return
	}
	s.writeJson(w, http.StatusOK, AuditListResponse{Items: records})
}

// 真的前端页面显示了
// 获取构建列表页的每一行
func (s *Server) buildSummary(ro *deliveryv1alpha1.Rollout) RolloutSummary {
	// 状态这是
	phase := string(ro.Status.Phase)
	if phase == "" {
		phase = "New"
	}
	// 步骤数
	stepCount := int32(0)
	if ro.Spec.Strategy.Canary != nil {
		// 没报错就获得用户定义的步骤的次数
		stepCount = int32(len(ro.Spec.Strategy.Canary.Steps))
	}
	return RolloutSummary{
		Namespace:        ro.Namespace,
		Name:             ro.Name,
		Phase:            phase,
		Message:          ro.Status.Message,
		Revision:         ro.Status.Revision,
		CurrentStepIndex: ro.Status.CurrentStepIndex,
		CurrentWeight:    ro.Status.CurrentWeight,
		StepCount:        stepCount,
		Replicas:         replicasOf(ro),
		Image:            imageOf(ro.Spec.Template.Spec.Containers),
		CreatedAt:        nowStamp(ro.CreationTimestamp.Time),
		UpdatedAt:        nowStamp(ro.CreationTimestamp.Time),
		Analysis:         buildMetricViews(ro, false),
	}
}

// 构建详情页全量数据:在Summary基础上，拉取底下的Pod和Deployment，和conditions
func (s *Server) buildDetail(ctx context.Context, ro *deliveryv1alpha1.Rollout) RolloutDetail {
	summary := s.buildSummary(ro)
	summary.Analysis = buildMetricViews(ro, true)

	detail := RolloutDetail{
		RolloutSummary:      summary,
		Spec:                specView(ro),
		Conditions:          conditionViews(ro),
		Pods:                s.podsOf(ctx, ro),
		Deployments:         s.deploymentsOf(ctx, ro),
		StableTemplateHash:  ro.Status.StableTemplateHash,
		CanaryTemplateHash:  ro.Status.CanaryTemplateHash,
		BlockedTemplateHash: ro.Status.BlockedTemplateHash,
		ObservedGeneration:  ro.Status.ObservedGeneration,
	}
	// 没有时间就给他设个现在的
	if ro.Status.StepStartedAt != nil {
		detail.StepStartedAt = nowStamp(ro.Status.StepStartedAt.Time)
	}
	if ro.Status.LastAnalysisAt != nil {
		detail.LastAnalysisAt = nowStamp(ro.Status.LastAnalysisAt.Time)
	}
	return detail
}

// 构建 Prometheus 指标面板:给用户呈现的spec配置和status的历史记录
func buildMetricViews(ro *deliveryv1alpha1.Rollout, withSample bool) []MetricView {
	// 定义空的指标切片
	views := make([]MetricView, 0)
	seen := map[string]bool{}

	var specMetrics []deliveryv1alpha1.AnalysisMetric
	if ro.Spec.Analysis != nil {
		// 获取用户配置的Analysis.Metrics
		specMetrics = ro.Spec.Analysis.Metrics
	}
	// 遍历
	for _, metric := range specMetrics {
		view := MetricView{
			Name:        metric.Name,
			Query:       metric.Query,
			Threshold:   metric.Threshold,
			Comparison:  string(analysis.ComparisonOf(metric)),
			Description: metric.Description,
			Samples:     []SampleView{},
		}
		if status := findAnalysisStatus(ro, metric.Name); status != nil {
			applyAnalysisStatus(&view, status, withSample)
		}
		seen[metric.Name] = true
		views = append(views, view)
	}
	// 不上spec中已删除但status中仍然保留历史采样的指标
	for i := range ro.Status.Analysis {
		status := &ro.Status.Analysis[i]
		if seen[status.Name] {
			continue
		}
		view := MetricView{Name: status.Name, Samples: []SampleView{}}
		applyAnalysisStatus(&view, status, withSample)
		views = append(views, view)
	}
	return views
}

// 拼接指标
func applyAnalysisStatus(view *MetricView, status *deliveryv1alpha1.MetricAnalysisStatus, withSamples bool) {
	// 获取status的successes、Failure
	view.Successes = status.Successes
	view.Failures = status.Failures
	if len(status.Samples) == 0 {
		return
	}
	last := status.Samples[len(status.Samples)-1]
	view.LatestValue = last.Value
	view.LatestAt = nowStamp(last.Time.Time)
	view.Passed = last.Passed

	if !withSamples {
		return
	}
	start := 0
	if len(status.Samples) > MaxSamplesPerMetric {
		start = len(status.Samples) - MaxSamplesPerMetric
	}
	for _, sample := range status.Samples[start:] {
		view.Samples = append(view.Samples, SampleView{
			Time:   nowStamp(sample.Time.Time),
			Value:  sample.Value,
			Passed: sample.Passed,
		})
	}
}

// 遍历Steps查询当前是SetWeight还是pause、analysis给前端
func specView(ro *deliveryv1alpha1.Rollout) SpecView {
	view := SpecView{
		Replicas:     replicasOf(ro),
		Port:         ro.Spec.Port,
		StrategyType: string(ro.Spec.Strategy.Type),
		Image:        imageOf(ro.Spec.Template.Spec.Containers),
	}
	// 判断Canary是否存在，存在就看看当前是啥状态
	if ro.Spec.Strategy.Canary != nil {
		view.DynamicStableScale = ro.Spec.Strategy.Canary.DynamicStableScale
		for i, step := range ro.Spec.Strategy.Canary.Steps {
			stepView := StepView{Index: int32(i)}
			switch {
			// 获取SetWeight状态并呈现给前端
			case step.SetWeight != nil:
				stepView.Kind = "setWeight"
				stepView.Weight = *step.SetWeight
			// 这个就是pause以下就是analysis
			case step.Pause != nil:
				stepView.Kind = "pause"
				stepView.DurationSeconds = int32(step.Pause.Duration.Duration.Round(time.Second) / time.Second)

			case step.Analysis != nil:
				stepView.Kind = "analysis"
				stepView.Metric = step.Analysis.Metrics
			}
			view.Steps = append(view.Steps, stepView)
		}
	}
	// 判断spec的Ingress、Analysis
	if ro.Spec.Ingress != nil {
		view.IngressPath = ro.Spec.Ingress.Path
		view.IngressHost = ro.Spec.Ingress.Host
	}

	if ro.Spec.Analysis != nil {
		view.AnalysisURL = ro.Spec.Analysis.PrometheusURL
		view.AnalysisIntervalSec = int32(analysis.IntervalOf(ro.Spec.Analysis).Seconds())
		view.SuccessThreshold = analysis.SuccessThresholdOf(ro.Spec.Analysis)
		view.FailureThreshold = analysis.FailureThresholdOf(ro.Spec.Analysis)
	}
	view.Metrics = buildMetricViews(ro, false)
	return view
}

func conditionViews(ro *deliveryv1alpha1.Rollout) []ConditionView {
	views := make([]ConditionView, 0, len(ro.Status.Conditions))
	for _, condition := range ro.Status.Conditions {
		views = append(views, ConditionView{
			Type:               condition.Type,
			Status:             string(condition.Status),
			Reason:             condition.Reason,
			Message:            condition.Message,
			LastTransitionTime: nowStamp(condition.LastTransitionTime.Time),
		})
	}
	return views
}

// 定义Pod显示的东西
func (s *Server) podsOf(ctx context.Context, ro *deliveryv1alpha1.Rollout) []PodView {
	var pods corev1.PodList
	if err := s.Client.List(ctx, &pods,
		client.InNamespace(ro.Namespace),
		// 通过label匹配
		client.MatchingLabels{rolloutcontroller.LabelApp: ro.Name},
	); err != nil {
		s.Logger.Error(err, "list pods failed", "rollout", ro.Name)
		return []PodView{}
	}
	views := make([]PodView, 0, len(pods.Items))
	for i := range pods.Items {
		pod := &pods.Items[i]
		view := PodView{
			Name:      pod.Name,
			Phase:     string(pod.Status.Phase),
			Image:     imageOf(pod.Spec.Containers),
			Track:     pod.Labels[rolloutcontroller.LabelTrack],
			PodIP:     pod.Status.PodIP,
			Node:      pod.Spec.NodeName,
			Ready:     podReadyStatus(pod),
			Restarts:  restartCount(pod),
			StartedAt: "",
		}
		if pod.Status.StartTime != nil {
			view.StartedAt = nowStamp(pod.Status.StartTime.Time)
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		return views[i].Name < views[j].Name
	})
	return views
}

// 这是Deployment相关的
func (s *Server) deploymentsOf(ctx context.Context, ro *deliveryv1alpha1.Rollout) []DeploymentView {
	names := []string{ro.Name, ro.Name + "-canary"}
	views := make([]DeploymentView, 0, len(names))
	for _, name := range names {
		dep := &appsv1.Deployment{}
		if err := s.Client.Get(ctx, client.ObjectKey{Namespace: ro.Namespace, Name: name}, dep); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			s.Logger.Error(err, "get deployment failed", "deployment", name)
			continue
		}
		views = append(views, DeploymentView{
			Name:       dep.Name,
			Track:      dep.Labels[rolloutcontroller.LabelTrack],
			Replicas:   derefInt32(dep.Spec.Replicas),
			Ready:      dep.Status.ReadyReplicas,
			Available:  dep.Status.AvailableReplicas,
			Updated:    dep.Status.UpdatedReplicas,
			Image:      imageOf(dep.Spec.Template.Spec.Containers),
			Generation: dep.Generation,
		})
	}
	return views
}

func findAnalysisStatus(ro *deliveryv1alpha1.Rollout, name string) *deliveryv1alpha1.MetricAnalysisStatus {
	for i := range ro.Status.Analysis {
		if ro.Status.Analysis[i].Name == name {
			return &ro.Status.Analysis[i]
		}
	}
	return nil
}

// 副本数
func replicasOf(ro *deliveryv1alpha1.Rollout) int32 {
	if ro.Spec.Replicas == nil {
		return 1
	}
	return *ro.Spec.Replicas
}

// 镜像
func imageOf(containers []corev1.Container) string {
	if len(containers) == 0 {
		return ""
	}
	return containers[0].Image
}

func derefInt32(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}

func podReadyStatus(pod *corev1.Pod) string {
	ready := 0
	total := len(pod.Status.ContainerStatuses)
	for _, status := range pod.Status.ContainerStatuses {
		if status.Ready {
			ready++
		}
	}
	if total == 0 {
		return "0/0"
	}
	return fmt.Sprintf("%d/%d", ready, total)
}

func restartCount(pod *corev1.Pod) int32 {
	var restarts int32
	for _, status := range pod.Status.ContainerStatuses {
		restarts += status.RestartCount
	}
	return restarts
}

func validAction(action string) bool {
	switch action {
	case rolloutcontroller.CommandPromote,
		rolloutcontroller.CommandAbort,
		rolloutcontroller.CommandPause,
		rolloutcontroller.CommandResume,
		rolloutcontroller.CommandRetry:
		return true
	default:
		return false
	}
}

func randomID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf)
}
