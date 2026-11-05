package controlplane

// 这个就是给API服务器搞得监控指标
// 它记录了这个 Web 服务器接收了多少请求、处理每个请求花了多长时间、用户在页面上点了多少次发布/回滚按钮、以及写入了多少条审计日志
// 比如需要：记录HTTP请求的总次数、HTTP请求的耗时分布、用户点了什么操作按钮（发布、回滚）、审计日志写了多少条
import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics控制台后端的独立指标注册表
type Metrics struct {
	registry      *prometheus.Registry
	requests      *prometheus.CounterVec
	durations     *prometheus.HistogramVec
	actions       *prometheus.CounterVec
	auditAppended prometheus.Counter
}

// 创建并注册控制台后端指标
func NewMetrics() *Metrics {
	// 创建新注册
	registry := prometheus.NewRegistry()

	m := &Metrics{
		registry: registry,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "controlplane",
			Name:      "http_requests_total",
			Help:      "Total HTTP requests handled by the controlplane server.",
			// 要的是method（get、Post）、route（路由）、status（状态码），这是主要用于后面计算QPS、错误率
		}, []string{"method", "route", "status"}),

		durations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "controlplane",
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency of the controlplane server.",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
			//Buckets 划定了时间耗时区间，比如是12ms就落在0.01, 0.025之间
		}, []string{"method", "route"}),

		actions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "controlplane",
			Name:      "rollout_actions_total",
			Help:      "Total rollout actions issued from the console.",
			// 做了什么操作，谁干的，结果怎么样
		}, []string{"action", "role", "result"}),

		auditAppended: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "controlplane",
			Name:      "audit_records_total",
			Help:      "Total audit records persisted.",
		}),
	}
	registry.MustRegister(
		m.requests,
		m.durations,
		m.actions,
		m.auditAppended,
		// 自动获取 Go 运行时的指标，比如当前开了多少 goroutine、用了多少内存（Heap）、垃圾回收（GC）停顿时间、CPU 占用率
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)
	return m
}

// 注册指标的方法
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// 记录HTTP请求的方法
func (m *Metrics) record(method, route string, status int, elapsed time.Duration) {
	m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc() //strconv.Itoa是把变量转换成字符串的。promenade是字符串形式存户
	m.durations.WithLabelValues(method, route).Observe(elapsed.Seconds()) // .Seconds() 转成浮点数（秒）
}

// 记录变更操作方法
func (m *Metrics) recordAction(action, role, result string) {
	m.actions.WithLabelValues(action, role, result).Inc()
}

// 审计写入操作
func (m *Metrics) recordAudit() {
	m.auditAppended.Inc()
}
