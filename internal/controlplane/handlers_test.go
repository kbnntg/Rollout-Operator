package controlplane

// 登陆验证各项内容
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	deliveryv1alpha1 "github.com/kbnntg/rollout-operator/api/v1alpha1"
	rolloutcontroller "github.com/kbnntg/rollout-operator/internal/controller"
)

// 搭建测试环境
func newTestServer(t *testing.T) (*Server, client.Client) {
	t.Helper()
	// 创建测试scheme
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("clientgo scheme: %v", err)
	}
	if err := deliveryv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("delivery scheme: %v", err)
	}

	// 创建一个rollout
	replicas := int32(2)
	rollout := &deliveryv1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		Spec: deliveryv1alpha1.RolloutSpec{
			Replicas: &replicas,
			Port:     80,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx:1.26"}}},
			},
			Strategy: deliveryv1alpha1.Strategy{
				Type:   deliveryv1alpha1.StrategyCanary,
				Canary: &deliveryv1alpha1.CanaryStrategy{Steps: []deliveryv1alpha1.Step{{SetWeight: &replicas}}},
			},
		},
		// 模拟是在发布中的
		Status: deliveryv1alpha1.RolloutStatus{
			Phase:            deliveryv1alpha1.PhaseProgressing,
			CurrentWeight:    10,
			CurrentStepIndex: 0,
			Revision:         1,
		},
	}
	kc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rollout).WithStatusSubresource(&deliveryv1alpha1.Rollout{}).Build()

	// 初始化鉴权配置
	auth, err := NewAuth("admin:admin123:admin, viewer:viewer123:viewer", testSecret, time.Hour)
	if err != nil {
		t.Fatalf("new auth: %v", err)
	}
	// 审计日志
	audit := NewAuditStore(kc, "rollout-system", "controlplane-audit")
	//内存文件系统模拟前端静态资源
	assets := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	return NewServer(kc, nil, auth, audit, assets, logr.Discard()), kc
}

// 辅助函数：向/api/login发送登录请求，返回200，提取出JWT Token
func login(t *testing.T, srv *Server, username, password string) string {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response LoginResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	// 返回Token
	return response.Token
}

// 构建请求，自动在Header中添加token
func authRequest(method, path, token, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// 登陆与身份获取
func TestLoginAndMe(t *testing.T) {
	srv, _ := newTestServer(t)
	token := login(t, srv, "admin", "admin123")

	// 带者Token请求/api/login
	req := authRequest(http.MethodGet, "/api/me", token, "")
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("me status = %d", recorder.Code)
	}
	var user UserInfo
	if err := json.Unmarshal(recorder.Body.Bytes(), &user); err != nil {
		t.Fatalf("decode me response: %v", err)
	}
	// 	解析出来的用户信息必须匹配
	if user.Name != "admin" || user.Role != RoleAdmin {
		t.Fatalf("user = %+v", user)
	}
}

// 错误密码测试
func TestLoginRejectBadPassword(t *testing.T) {
	srv, _ := newTestServer(t)
	body := `{"username":"admin","password":"nope"}`
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	// 错误密码必须返回401
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

// 测试获取查询列表
func TestListRollouts(t *testing.T) {
	srv, _ := newTestServer(t)
	// 先用Viewer
	token := login(t, srv, "viewer", "viewer123")

	// 请求路径
	req := authRequest(http.MethodGet, "/api/rollouts", token, "")
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)

	// 必须正确
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Items []RolloutSummary `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(response.Items) != 1 || response.Items[0].Name != "demo" {
		t.Fatalf("unexpected items: %+v", response.Items)
	}
	// 验证状态是不是Progressing
	if response.Items[0].Phase != string(deliveryv1alpha1.PhaseProgressing) {
		t.Fatalf("unexpected phase: %s", response.Items[0].Phase)
	}
}

// 测试权限控制与下发指令
func TestActionRequiresAdmin(t *testing.T) {
	srv, kc := newTestServer(t)
	// 访客Token
	viewerToken := login(t, srv, "viewer", "viewer123")
	body := `{"action":"promote"}`
	// 用访客请求路径
	req := authRequest(http.MethodPost, "/api/rollouts/default/demo/actions", viewerToken, body)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	// 权限肯定不够，直接返回403
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("viewer action status = %d, want 403", recorder.Code)
	}

	// 换成admin管理员
	adminToken := login(t, srv, "admin", "admin123")
	req = authRequest(http.MethodPost, "/api/rollouts/default/demo/actions", adminToken, body)
	recorder = httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	// 这下可以了
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("admin action status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	ro :=
		&deliveryv1alpha1.Rollout{}
	if err := kc.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "demo"}, ro); err != nil {
		t.Fatalf("get rollout: %v", err)
	}
	if ro.Annotations[rolloutcontroller.AnnotationCommand] != "promote" {
		t.Fatalf("command annotation = %q", ro.Annotations[rolloutcontroller.AnnotationCommand])
	}

	// 审计日志看看是否写进去了
	req = authRequest(http.MethodGet, "/api/audit", adminToken, "")
	recorder = httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	var auditResponse AuditListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &auditResponse); err != nil {
		t.Fatalf("decode audit: %v", err)
	}
	// 审计日志长度为1，且Action为"promote"
	if len(auditResponse.Items) != 1 || auditResponse.Items[0].Action != "promote" {
		t.Fatalf("unexpected audit records: %+v", auditResponse.Items)
	}
}

// 测试动作是否存在
func TestActionRejectsUnknownAction(t *testing.T) {
	srv, _ := newTestServer(t)
	token := login(t, srv, "admin", "admin123")
	// 弄了个不存在的动作
	req := authRequest(http.MethodPost, "/api/rollouts/default/demo/actions", token, `{"action":"explode"}`)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	// 返回报错
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

// 详情页数据聚合
func TestRolloutDetailIncludesPodsAndSteps(t *testing.T) {
	srv, kc := newTestServer(t)
	token := login(t, srv, "admin", "admin123")

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-abc",
			Namespace: "default",
			Labels: map[string]string{
				rolloutcontroller.LabelApp:   "demo",
				rolloutcontroller.LabelTrack: rolloutcontroller.TrackStable,
			},
		},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx:1.26"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if err := kc.Create(context.Background(), pod); err != nil {
		t.Fatalf("create pod: %v", err)
	}
	req := authRequest(http.MethodGet, "/api/rollouts/default/demo", token, "")
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var detail RolloutDetail
	if err := json.Unmarshal(recorder.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	// Spec里的Steps是否正确解析成了前端的StepView
	if len(detail.Spec.Steps) != 1 || detail.Spec.Steps[0].Kind != "setWeight" {
		t.Fatalf("unexpected steps: %+v", detail.Spec.Steps)
	}
	//底层的Pod列表是否被成功查出并挂载到Detail视图上
	if len(detail.Pods) != 1 || detail.Pods[0].Name != "demo-abc" {
		t.Fatalf("unexpected pods: %+v", detail.Pods)
	}
}
