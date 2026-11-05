package controlplane

// 这个是创建json/错误相应，规范化静态资源路径，按扩展名给Content-Type，可测试函数与获取状态码给Metrics指标，还要用flusher确保SSE可用

import (
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"time"
)

// 创建Json、错误响应
func (s *Server) writeJson(w http.ResponseWriter, status int, payload interface{}) {
	// 设置Header头
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// 写入状态码
	w.WriteHeader(status)
	if payload == nil {
		return
	}

	_ = json.NewEncoder(w).Encode(payload)
}

// 错误响应
func (s *Server) withError(w http.ResponseWriter, status int, message string){
	s.writeJson(w, status, map[string]string("error": message))
}

// 对路径进行规范，防止路径遍历
func Path(urlPath string) string{
	// 对获取的路径URL进行+/与清除
	cleaned := strings.TrimPrefix(path.Clean("/" + urlPath), "/")
	// 会清除../前的根如果是个/index这种类型就默认返回个index.html
	if cleaned == ""{
		return "index.html"
	}
	return cleaned
}

// 获取静态资源加入到Content-Type
func contentTypeOf(name string) string{
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(lower, ".js"), strings.HasSuffix(lower, ".mjs"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(lower, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(lower, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".woff2"):
		return "font/woff2"
	case strings.HasSuffix(lower, ".ico"):
		return "image/x-icon"
	case strings.HasSuffix(lower, ".json"):
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

// 测试替换时间函数
var (
	nowFunc = time.Now
	sinceFunc = time.Since
)

// 定义一个statusRecorder的结构体，用于后续找到status存入进writeHeader中，如果不定义不好接受
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// 接收状态码并放入到WriteHeader中
func (r *statusRecorder)WriteHeader(code int){
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush事先http.Flusher，报证SSE可以正常流通
func (r *statusRecorder) Flush(){
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
