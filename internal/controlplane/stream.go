package controlplane

// 这个模块是通过SSE相应接收实时日志
import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	deliveryv1alpha1 "github.com/kbnntg/rollout-operator/api/v1alpha1"
)

// 定义心跳
const Interval = 15 * time.Second

// 以SSE方式推送集群内的ROllout
// 判断有没有SSE功能
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.withError(w, http.StatusInternalServerError, "不支持SSE")
		return
	}

	// 加Header
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	// 获取上线问
	ctx := r.Context()
	// 通过watch监听Event时间
	watcher, err := s.Watch.Watch(ctx, &deliveryv1alpha1.RolloutList{})
	if err != nil {
		s.Logger.Error(err, "watch rollouts failed")
		fmt.Fprintf(w, "event: error\ndata: {\"error\":%q}\n\n", err.Error())
		flusher.Flush()
		return
	}
	// 监听完要结束的进程的话别忘了关
	defer watcher.Stop()

	// ping
	ping := time.NewTicker(Interval)
	defer ping.Stop()

	// 并发处理请求，用户关闭浏览器、停止心跳、获取日志
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case event, open := <-watcher.ResultChan():
			if !open {
				return
			}
			payload, err := json.Marshal(map[string]interface{}{
				"type":   string(event.Type),
				"object": event.Object,
			})
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: rollout\ndata: %s\n\n", payload)
			flusher.Flush()
		}
	}
}
