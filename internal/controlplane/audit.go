package controlplane

// 接收前端发来的审计日志并记录到ConfigMap中，后期可以换为MySQL或Redis分离
import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// 定义默认存储日志和默认行数
const (
	auditDataKey = "audit.log"
	// 默认行数，configmap最多只能有1.5MB，设置2000行
	defaultMaxAuditLines = 2000
)

// 创建将审计日志以jsonLines形式持久到ConfigMap的结构体
type AuditStore struct {
	client    client.Client
	namespace string
	name      string
	maxLines  int
	mu        sync.Mutex // 互斥锁,不加多协程操作的话可能会崩溃
}

// 创建构建审计日志存储的方法
func NewAuditStore(c client.Client, namespace, name string) *AuditStore {
	if name == "" {
		// 默认这个名
		name = "controlplane-audit"
	}
	return &AuditStore{client: c, namespace: namespace, name: name, maxLines: defaultMaxAuditLines}
}

// 写入日志的方法
func (s *AuditStore) Append(ctx context.Context, record AuditRecord) error {
	// 先设置互斥锁
	s.mu.Lock()
	defer s.mu.Unlock()

	// json.Marshal(record)把一条审计记录变成一行JSON字符串
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	// 查找有没有这个configmap，没有就创建
	key := client.ObjectKey{Namespace: s.namespace, Name: s.name}
	cm := &corev1.ConfigMap{}
	if err := s.client.Get(ctx, key, cm); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		// 第一次就创建，把json按换行符弄进去
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: s.name, Namespace: s.namespace},
			Data:       map[string]string{auditDataKey: string(line) + "\n"},
		}
		return s.client.Create(ctx, cm)
	}

	// lines调用这个函数对数据按换行符分开
	lines := parseAuditLines(cm.Data[auditDataKey])
	lines = append(lines, string(line))
	if len(lines) > s.maxLines {
		lines = lines[len(lines)-s.maxLines:]
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	// 将新日志追加进去
	cm.Data[auditDataKey] = strings.Join(lines, "\n") + "\n"
	return s.client.Update(ctx, cm)
}

// List返回审计记录（按时间排序）
func (s *AuditStore) List(ctx context.Context, namespace string, limit int) ([]AuditRecord, error) {
	// 前端要展示日志列表，调用这个方法
	// 还是找k8s的configmap没有就返回一个空
	key := client.ObjectKey{Namespace: s.namespace, Name: s.name}
	cm := &corev1.ConfigMap{}
	if err := s.client.Get(ctx, key, cm); err != nil {
		if apierrors.IsNotFound(err) {
			return []AuditRecord{}, nil
		}
		return nil, err
	}
	// 默认显示200行
	if limit <= 0 {
		limit = 200
	}

	// 按行分开日志
	rawLines := parseAuditLines(cm.Data[auditDataKey])
	records := make([]AuditRecord, 0, len(rawLines))
	for i := len(rawLines) - 1; i >= 0; i-- {
		var record AuditRecord
		// 把每行日志存入到record地址中某一行格式坏了（比如被人工修改了），continue跳过，为什么这样，这是为了获取最新的审计日志才重新加入的
		if err := json.Unmarshal([]byte(rawLines[i]), &record); err != nil {
			continue
		}
		if namespace != "" && record.Namespace != namespace {
			continue
		}
		records = append(records, record)
		if len(records) >= limit {
			break
		}
	}
	return records, nil
}
func parseAuditLines(data string) []string {
	if strings.TrimSpace(data) == "" {
		return nil
	}
	parts := strings.Split(strings.TrimRight(data, "\n"), "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return out
}
