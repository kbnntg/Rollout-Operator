package main

// 定义Reconcile器，充当接收客户端、scheme、事件的控制器，从Manger注入到Reconcile，Reconcile再注册回Manger

// 一、导入包
import (
	"flag" // Go 自带的命令行参数解析（类似 Python 的 argparse）
	"os"   // 操作系统接口，用来退出程序

	// 下面 4 个是 K8s 官方库
	"k8s.io/apimachinery/pkg/runtime"                                 // 核心：Scheme 翻译字典
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"                // 工具包，帮我们处理报错
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"               // K8s 原生资源（Pod, Deployment 等）的字典
	ctrl "sigs.k8s.io/controller-runtime"                             // 主角：Operator 开发框架
	"sigs.k8s.io/controller-runtime/pkg/healthz"                      // 健康检查
	"sigs.k8s.io/controller-runtime/pkg/log/zap"                      // 日志
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server" // 监控指标服务器
	"sigs.k8s.io/controller-runtime/pkg/webhook"                      // 准入校验拦截器

	// 下面 3 个是你自己项目里的代码
	deliveryv1alpha1 "github.com/kbnntg/rollout-operator/api/v1alpha1"   // 你刚写的 Rollout 结构体
	"github.com/kbnntg/rollout-operator/internal/controller"             // 马上要写的控制器
	rolloutwebhook "github.com/kbnntg/rollout-operator/internal/webhook" // 马上要写的 Webhook
)

// 二、创建scheme并且定义属性
var (
	scheme = runtime.NewScheme()
	// 下列用户启动程序时传来的参数
	metricsAddr          string // 监控指标端口
	probeAddr            string // 健康检查端口
	enableLeaderElection bool   // 是否开启选主
	enableWebhooks       bool   // 是否开启门卫验证
	webhookCertDir       string // webhooks证书位置
	webhookPort          int    // webhook监听端口
)

// 三、init初始化，登记到scheme中
func init() {
	// 1.把k8s原生资源加到字典里
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	// 2.自定义的rollout资源登记进去
	utilruntime.Must(deliveryv1alpha1.AddToScheme(scheme))
}

// 四、定义启动属性
func main() {
	// 定义参数。比如用户敲 go run main.go --metrics-bind-address=:9090
	// flag 就会把 ":9090" 存进 metricsAddr 变量里。第二个参数是默认值。
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Prometheus 指标监听地址，0 表示关闭。")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "健康检查监听地址。")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "是否启用选主...")
	flag.BoolVar(&enableWebhooks, "enable-webhooks", true, "是否启用默认值/校验 webhook。")
	flag.StringVar(&webhookCertDir, "webhook-cert-dir", "/tmp/k8s-webhook-server/serving-certs", "webhook 服务证书目录。")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "webhook 服务监听端口。")

	// 配置日志（让日志好看点，别像一坨乱码）
	logOpts := zap.Options{Development: false}
	logOpts.BindFlags(flag.CommandLine) // 允许用户在命令行里也修改日志配置

	flag.Parse()                                          // 真正开始解析上面的参数
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&logOpts))) // 设置全局日志

	// 获取一个带 "setup" 前缀的日志记录器，打出来的日志会像：[setup] xxxx
	logger := ctrl.Log.WithName("setup")
	// 创建Manager，管制怎么连k8s、监听事件、监控端口、健康检查
	managerOptions := ctrl.Options{
		Scheme:                 scheme,                                          // 刚才的字典
		Metrics:                metricsserver.Options{BindAddress: metricsAddr}, // 监控指标
		HealthProbeBindAddress: probeAddr,                                       // 探针端口
		LeaderElection:         enableLeaderElection,                            // 是否开启选主
		LeaderElectionID:       "rollout-operator.delivery.sre.dev",             // 主锁的名字
	}
	// 如果开启webhooks就配置端口和证书
	if enableWebhooks {
		managerOptions.WebhookServer = webhook.NewServer(webhook.Options{
			Port:    webhookPort,
			CertDir: webhookCertDir,
		})
	}

	// 创建mgr链接到K8s的配置文件
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), managerOptions)
	if err != nil {
		// 没连上就报错
		logger.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// 注册业务逻辑Reconciler，把mgr的client、scheme、EventRecorder（用来及事件）都塞给他
	reconciler := &controller.RolloutReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("rollout-operator"),
	}

	// 告诉mgr，以后只要Rollout资源有变化，就交给reconciler
	if err := reconciler.SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller")
		os.Exit(1)
	}

	// 如果开启Webhook，就把它也注册进mgr
	if enableWebhooks {
		rolloutwebhook.Register(mgr)
		logger.Info("admission webhooks registered")
	}
	// K8s 会定时访问 /healthz 和 /readyz 接口，看你程序死没死
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		logger.Error(err, "unable to set up ready check")
		os.Exit(1)
	}
	logger.Info("starting manager")
	// SetupSingnalHandler()：如果你按了Ctrl+C，程序不会立刻暴毙
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "manager exited with error")
		os.Exit(1)
	}
}
