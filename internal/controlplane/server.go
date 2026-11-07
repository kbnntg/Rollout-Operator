package controlplane

// 该模块就是把auth（认证）、audit（审计）、metrics（API 服务监控）全部拼接一起
import (
	"context"
	"io/fs"
	"net/http"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// 创建Server结构体
type Server struct {
	Client    client.Client    // 客户端
	Watch     client.WithWatch // Watch观测
	Auth      *Auth            // 前面定义的认证模块
	Audit     *AuditStore      // 审计日志模块
	Metrics   *Metrics         // API服务器监控模块
	WebAssets fs.FS            // 就当接收前端打包的文件
	Logger    logr.Logger      // 日志
}

// 构建控制台后端方法
func NewServer(c client.Client, watch client.WithWatch, auth *Auth, audit *AuditStore, webAssets fs.FS, logger logr.Logger) *Server {
	// 给结构体定义的，接收外部依赖，填进结构体
	return &Server{
		Client:    c,
		Watch:     watch,
		Auth:      auth,
		Audit:     audit,
		WebAssets: webAssets,
		Metrics:   NewMetrics(),
		Logger:    logger,
	}
}

// Handler方法定义中间件完整路由
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// 先定义一个框架，后续传入到对应方法的时候会调用
	mux.HandleFunc("GET /api/healthz", s.handleHealthz)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.Handle("GET /api/me", s.requireAuth(http.HandlerFunc(s.handleMe)))
	mux.Handle("GET /api/rollouts", s.requireAuth(http.HandlerFunc(s.handleListRollouts)))
	mux.Handle("GET /api/rollouts/{namespace}/{name}", s.requireAuth(http.HandlerFunc(s.handleRolloutDetail)))
	mux.Handle("POST /api/rollouts/{namespace}/{name}/actions", s.requireAdmin(http.HandlerFunc(s.handleAction)))
	mux.Handle("GET /api/audit", s.requireAuth(http.HandlerFunc(s.handleAudit)))
	mux.Handle("GET /api/events", s.requireAuth(http.HandlerFunc(s.handleEvents)))
	mux.Handle("GET /metrics", s.Metrics.Handler())
	mux.Handle("/", s.static())

	// 从外到里接收，从里到外调用
	// recoverer放崩溃、withCORS跨域、WithMetrics监控、accessLog写入日志
	return s.recoverer(s.withCORS(s.withMetrics(s.accessLog(mux))))
}

// 用户信息塞进Context
type claimsContextKey struct {
}

// 将验证信息传入到这个方法中
func withClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey{}, claims)
}

// 取出信息,requireAuth用withClaims 把用户信息塞进Context 后面的Handler用claimsFrom把用户信息取出来
func claimsFrom(ctx context.Context) *Claims {
	claims, _ := ctx.Value(claimsContextKey{}).(*Claims)
	return claims
}

// 对传来的信息Token进行验证，在Handle方法中用到这个中间件的都要验证
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 获取token
		token := BearerToken(r)
		if token == "" {
			s.withError(w, http.StatusUnauthorized, "没有Token")
			return
		}
		// 验证Token的结果
		claims, err := s.Auth.Parse(token)
		if err != nil {
			s.withError(w, http.StatusUnauthorized, "Token可能是笑了")
			return
		}
		// 验证成功了，就放在内存里，等待下一步
		next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), claims)))
	})
}

// 验证是不是管理员
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 获取requireAuth存入到claimsFrom中的token
		claims := claimsFrom(r.Context())
		if claims == nil || claims.Role != RoleAdmin {
			s.withError(w, http.StatusForbidden, "需要管理员权限")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (s *Server) handleHealthz(writer http.ResponseWriter, _ *http.Request) {
	// 健康检查
	s.writeJson(writer, http.StatusOK, map[string]string{"status": "ok"})
}

// 日志获取
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Logger.V(1).Info("http request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

// 监控
func (s *Server) withMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := nowFunc()

		next.ServeHTTP(recorder, r)

		// 这个是接收的路由模板，比如穿的是/api/rollouts/default/my-app接收的就是：/api/rollouts/{namespace}/{name}，自动获取namespace和name
		route := r.URL.Path
		if r.Pattern != "" {
			route = r.Pattern
		}
		// 写入到Metric文件的record中
		s.Metrics.record(r.Method, route, recorder.status, sinceFunc(start))
	})
}

// 跨域
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// 静态资源
func (s *Server) static() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 获取前端打包的文件，尝试读index.html，如果读不到说明前端没打包，直接报错
		asset, err := fs.ReadFile(s.WebAssets, "index.html")

		if err != nil {
			http.Error(w, "web console assets are not embedded; build the frontend first (make web)", http.StatusServiceUnavailable)
			return
		}

		// 如果请求的不是index.html（比如请求了app.js）
		if assetPath := Path(r.URL.Path); assetPath != "index.html" {
			if data, err := fs.ReadFile(s.WebAssets, assetPath); err != nil {
				// 加上头、缓存时间
				w.Header().Set("Content-Type", contentTypeOf(assetPath))
				w.Header().Set("Cache-Control", "publish, max-age=3600")
				_, _ = w.Write(data)
				return
			}
		}
		// 没有就一律返回index.html
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(asset)
	})
}

// 放崩溃
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.Logger.Error(nil, "panic while serving request", "path", r.URL.Path, "panic", rec)
				s.withError(w, http.StatusInternalServerError, "服务内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
