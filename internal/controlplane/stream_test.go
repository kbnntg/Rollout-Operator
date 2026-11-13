package controlplane

// SSE测试
import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestEventsStreamKeepsFlusher(t *testing.T) {
	srv, kc := newTestServer(t)
	watchClient, ok := kc.(client.WithWatch)
	if !ok {
		t.Fatalf("fake client does not implement client.WithWatch")
	}
	// watch监听
	srv.Watch = watchClient

	token := login(t, srv, "admin", "admin123")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(recorder, req)
		close(done)
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("event stream did not stop after context cancellation")
	}

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if ct := recorder.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
}
