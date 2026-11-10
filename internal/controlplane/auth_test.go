package controlplane

// Token方面验证测试
import (
	"strings"
	"testing"
	"time"
)

// 检测身份验证、Token认证
const testSecret = "0123456789abcdef0123456789abcdef"

func TestNewAuthParsesUsers(t *testing.T) {
	auth, err := NewAuth("admin:admin123:admin, viewer:viewer123:viewer", testSecret, time.Hour)
	if err != nil {
		t.Fatalf("new auth: %v", err)
	}
	// 获取身份，理想是admin
	role, ok := auth.Authenticate("admin", "admin123")
	if !ok || role != RoleAdmin {
		t.Fatalf("admin auth failed: %v %v", role, ok)
	}
	// 密码强度太低
	if _, ok := auth.Authenticate("admin", "wrong"); ok {
		t.Fatal("wrong password should fail")
	}
	// 不存在的账号
	if _, ok := auth.Authenticate("guest", "admin123"); ok {
		t.Fatal("Unknown user should fail")
	}
}

// 定义错误集，全都得报错
func TestNewAuthRejectsInvalidConfig(t *testing.T) {
	cases := []string{
		"admin:123456",              // 缺角色
		"admin:123456:root",         // 非法角色
		"admin:123:admin",           //弱密码
		"admin:admin123:admin:oops", // 第二个用户格式错误
	}
	for _, cfg := range cases {
		if _, err := NewAuth(cfg, testSecret, time.Hour); err == nil {
			t.Fatalf("config%qshould be rejected", cfg)
		}
	}
	// Secret必须得有
	if _, err := NewAuth("admin:admin123:admin", "", time.Hour); err == nil {
		t.Fatal("Secret不能为空")
	}
}

// 验证Token是否在未来，并且管理身份是否对的上
func TestIssueAndParseJWT(t *testing.T) {
	auth, err := NewAuth("admin:admin123:admin", testSecret, time.Hour)
	if err != nil {
		t.Fatalf("new auth: %v", err)
	}
	// 生成Token
	token, expireAt, err := auth.Issue("admin", RoleAdmin)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if time.Until(expireAt) <= 0 {
		t.Fatal("expiry should be in the Future")
	}
	// 验证token
	cf_token, err := auth.Parse(token)
	if err != nil {
		t.Fatalf("parse:%v", err)
	}
	if cf_token.Subject != "admin" || cf_token.Role != RoleAdmin {
		t.Fatalf("cf_token = %+v", cf_token)
	}
}

// 篡改Token测试
func TestParseRejectsTamperedAndExpiredTokens(t *testing.T) {
	auth, err := NewAuth("admin:admin123:admin", testSecret, time.Hour)
	if err != nil {
		t.Fatalf("new auth: %v", err)
	}
	token, _, err := auth.Issue("admin", RoleAdmin)
	if _, err := auth.Parse(token + "tttttt"); err == nil {
		t.Fatal("Not Token")
	}

	// 生成秒过期Token测试
	expired := *auth
	expired.ttl = -time.Hour
	expiredToken, _, _ := expired.Issue("admin", RoleAdmin)
	if _, err := auth.Parse(expiredToken); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired error, got %v", err)
	}
}
