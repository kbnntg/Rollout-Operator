package main

// 解析配置、初始化各种依赖（K8s客户端、鉴权、审计等）、将前端代码打包进二进制，并启动可以优雅停机的HTTP服务

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	deliveryv1alpha1 "github.com/kbnntg/rollout-operator/api/v1alpha1"
	"github.com/kbnntg/rollout-operator/internal/controlplane"
)

//go:embed all:webdist
var webAssets embed.FS // 这个是将前端的所有文件直接打入到二进制文件中

// 注册表，只认识注册过的资源
var scheme = runtime.NewScheme()

func init() {
	// 将自己创建Rollout和源资源（Pod、Deployment）注册到注册表
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(deliveryv1alpha1.AddToScheme(scheme))
}

func main() {
	// 创建一堆要用的变量
	var (
		addr      string
		namespace string
		users     string
		jwtSecret string
		tokenTTL  time.Duration
		webDir    string
	)

	// 读取
	flag.StringVar(&addr, "addr", ":8090", "控制台后端监听地址")
	flag.StringVar(&namespace, "namespace", "rollout-system", "控制台所在命名空间，保存审计日志Configmap")
	flag.StringVar(&users, "users", envOr("CONTROLPLANE_USERS", ""), "用户配置，格式 user:password:role,user2:password2:role2")
	flag.StringVar(&jwtSecret, "jwt-secret", envOr("CONTROLPLANE_JWT_SECRET", ""), "JWT 签名密钥，生产环境必须通过 Secret 注入")
	flag.DurationVar(&tokenTTL, "token-ttl", 12*time.Hour, "登陆token有效期")
	flag.StringVar(&webDir, "web-dir", "", "外部前端静态目录")

	// 日志库
	logOpts := zap.Options{Development: false}
	// 把日志相关的参数也加进去了
	logOpts.BindFlags(flag.CommandLine)
	// 读取在终端里敲的整行命令
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&logOpts)))
	logger := ctrl.Log.WithName("controlplane")

	// 看看有没有JWT没有就创建一个随机的
	if jwtSecret == "" {
		jwtSecret = randomSecret()
		logger.Info("JWT secret not provided, generated a random one; restart will invalidate existing tokens")
	}
	if len(jwtSecret) < 16 {
		// 小于16位就报错
		logger.Error(fmt.Errorf("jwt secret too short"), "--jwt-secret must be at least 16 characters")
		os.Exit(1)
	}

	// 调用auth模块的NewAuth获取一个认证密钥
	auth, err := controlplane.NewAuth(users, jwtSecret, tokenTTL)
	if err != nil {
		logger.Error(err, "invalid user configuration")
		os.Exit(1)
	}

	// 读取K8s环境的配置文件
	config, err := ctrl.GetConfig()
	if err != nil {
		logger.Error(err, "unable to load kubeconfig")
		os.Exit(1)
	}

	// 像是什么Pod、Deployment就是kubeClient资源
	kubeClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		logger.Error(err, "unable to create kubernetes Client")
		os.Exit(1)
	}
	// Watch监听SSE
	watchClient, err := client.NewWithWatch(config, client.Options{Scheme: scheme})
	if err != nil {
		logger.Error(err, "unable to create watch Client")
		os.Exit(1)
	}
	// 审计
	auditStore := controlplane.NewAuditStore(kubeClient, namespace, "controlplane-audit")
	assets, err := loadWebAssets(webDir)
	if err != nil {
		logger.Error(err, "unable to load web assets")
		os.Exit(1)
	}

	//把K8s客户端、鉴权、审计、前端文件全塞给Server,之前的server结构体
	server := controlplane.NewServer(kubeClient, watchClient, auth, auditStore, assets, logger)

	// HTTP服务
	httpserver := &http.Server{
		Addr:              addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// signal.NetifyContext监听Ctrl+C与K8s发来的停机信号
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		// 收到停机信号等个10秒
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		logger.Info("shutting down controlplane gracefully")
		_ = httpserver.Shutdown(shutdownCtx)
	}()

	logger.Info("starting controlplane", "addr", addr)
	// 阻塞启动服务
	if err := httpserver.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error(err, "controlplane exited with error")
		os.Exit(1)
	}
}

func loadWebAssets(webdir string) (fs.FS, error) {
	if webdir != "" {
		return os.DirFS(webdir), nil
	}
	// 剥离路径前缀
	return fs.Sub(webAssets, "webdist")
}

// env环境接受对应的key如果key里有值就直接返回，没有就返回默认值(空字符串）
func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// 生成随机Secret
func randomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
