package analysis

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/common/model"

	deliveryv1alpha1 "github.com/kbnntg/rollout-operator/api/v1alpha1"
)

// Prometheus相关测试，返回的向量、矩阵啥的看看结果
// 测试标量、向量（取最大值）、空向量、矩阵（去最后一个点）等各种情况
func TestExtractValue(t *testing.T) {
	test := []struct {
		name    string
		value   model.Value
		want    float64
		wantErr bool
	}{
		// 标量直接显示了
		{name: "scalar", value: &model.Scalar{Value: 3.5}, want: 3.5},
		{
			// 向量取最大值
			name: "vector takes max",
			value: model.Vector{
				{Metric: model.Metric{}, Value: 1.3},
				{Metric: model.Metric{}, Value: 4.8},
			},
			want: 4.8,
		},
		// 矩阵取最后一个点
		{name: "empty vector", value: model.Vector{}, wantErr: true},
		{
			name:  "matrix last point",
			value: model.Matrix{{Values: []model.SamplePair{{Value: 1}, {Value: 2}}}},
			want:  2,
		},
	}
	for _, tt := range test {
		t.Run(tt.name, func(t *testing.T) {
			// 把遍历的值传入Prometheus模块的extractValue中，用来判断是向量、标量还是矩阵
			got, err := extractValue(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got value%v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// 判断
func TestPass(t *testing.T) {
	if !Pass(0.01, 0.05, deliveryv1alpha1.ComparisonLE) {
		t.Fatal("0.01 <= 0.05 should pass")
	}
	if Pass(0.5, 0.05, deliveryv1alpha1.ComparisonLE) {
		t.Fatal("0.5<=0.05 should fail")
	}
	if !Pass(0.9, 0.8, deliveryv1alpha1.ComparisonGE) {
		t.Fatal("0.9 >= 0.8 should pass")
	}
}

// 起一个临时假Prometheus
func TestQueryAgainstPrometheusHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,"0.25"]}]}}`)
	}))
	defer server.Close()

	// 创建客户端
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatalf("new Client:%v", err)
	}
	// 查询结果
	value, err := client.Query(context.Background(), "sum(rate(error[1m]))")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// 看看客户端能不能把字符串解析成浮点数
	if value != 0.25 {
		t.Fatalf("got %v, want 0.25", value)
	}
}

func TestNewClientRejectsEmptyAddress(t *testing.T) {
	// 空IP判断
	if _, err := NewClient(""); err == nil {
		t.Fatal("expected error for empty address")
	}
}

// 测测配置的那些默认值有没有生效（successThresholdOf、FailureThresholdOf、IntervalOf）
func TestThresholdDefaults(t *testing.T) {
	spec := &deliveryv1alpha1.AnalysisSpec{}
	if SuccessThresholdOf(spec) != 3 {
		t.Fatalf("default success threshold should be 3")
	}
	if FailureThresholdOf(spec) != 2 {
		t.Fatalf("default failure threshold should 2")
	}
	if IntervalOf(spec).Seconds() != 10 {
		t.Fatalf("default interval should be 10s")
	}

}
