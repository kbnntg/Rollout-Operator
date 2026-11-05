package controlplane

// 这个模块获取前端发来的用户、密码、role创建一个token，之后接收前端发送的token进行验证
import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// 角色定义
const (
	// RoleAdmin管理员
	RoleAdmin = "admin"
	// RoleViewer只读用户：只能查看
	RoleViewer = "viewer"
)

// Claims JWt载荷
type Claims struct {
	Subject   string `json:"sub"`  //用户名
	Role      string `json:"role"` // 角色身份
	IssuedAt  int64  `json:"iat"`  // 开始时间
	ExpiresAt int64  `json:"exp"`  // 结束时间
}

type userRecord struct {
	role string
	hash [32]byte
}

// 用户认证与角色判断
type Auth struct {
	secret []byte                // 签发和校验签名的密钥
	users  map[string]userRecord // 用户列表（内存缓存）
	ttl    time.Duration
}

// 解析user:password:role,...形式的用户配置并生成认证其
func NewAuth(usersConfig, secret string, ttl time.Duration) (*Auth, error) {
	if secret == "" {
		return nil, errors.New("jwt secret must not be null")
	}
	if ttl < 0 {
		ttl = 12 * time.Hour
	}
	// 生成默认账号密码身份
	if strings.TrimSpace(usersConfig) == "" {
		usersConfig = "admin:admin123:admin,viewer:viewer123:viewer"
	}
	// 创建空map
	users := map[string]userRecord{}
	// 用,切割usersconfig遍历
	for _, entry := range strings.Split(usersConfig, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid user entry%q,want user:password:role", entry)
		}
		// 分别获取name、password、role
		name, password, role := parts[0], parts[1], parts[2]
		if role != RoleAdmin && role != RoleViewer {
			return nil, fmt.Errorf("invalid role%qforuser%q,want admin or viewer", role, name)
		}
		if len(password) < 6 {
			return nil, fmt.Errorf("密码必须大于6位")
		}
		// 账号存入role，密码存入搭配hash中userRecord
		users[name] = userRecord{role: role, hash: sha256.Sum256([]byte(password))}
	}
	if len(users) == 0 {
		return nil, errors.New("no users configured")
	}
	return &Auth{secret: []byte(secret), users: users, ttl: ttl}, nil
}

// TTL 返回 token 有效期。
func (a *Auth) TTL() time.Duration {
	return a.ttl
}

// 校验用户密码，成功返回角色
func (a *Auth) Authenticate(username, password string) (string, bool) {
	record, ok := a.users[username]
	if !ok {
		return "", false
	}
	sum := sha256.Sum256([]byte(password))
	// 用了 ConstantTimeCompare 而不是 == 比较
	if subtle.ConstantTimeCompare(sum[:], record.hash[:]) != 1 {
		return "", false
	}
	return record.role, true
}

// Issue方法给用户签发JWT
func (a *Auth) Issue(username, role string) (string, time.Time, error) {
	// 获取当前时间
	now := time.Now()
	claims := Claims{
		// 校验体
		Subject:   username,
		Role:      role,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(a.ttl).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", time.Time{}, err
	}

	// 弄header
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	// payload用户体
	body := base64.RawURLEncoding.EncodeToString(payload)
	// 拼接起来
	sin := header + "." + body

	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(sin))
	sinture := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return sin + "." + sinture, now.Add(a.ttl), nil
}

// Parse校验并解析Jwt
func (a *Auth) Parse(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	// 接收到token，并且分解header + payload + Signature
	if len(parts) != 3 {
		return nil, errors.New("malformed token")
	}
	sin := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(sin))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	// 验证Signature的
	if subtle.ConstantTimeCompare([]byte(expected), []byte(parts[2])) != 1 {
		return nil, errors.New("invalid token signature")
	}

	// 把里面的 JSON 拿出来，检查一下是不是过期了，以及身份信息是不是合法
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("invalid token payload")
	}
	var claims Claims
	//把刚才解出来的 JSON 字节流 payload，塞进 claims 这个结构体的内存地址（&claims）里。
	//JSON 里的 sub、role、exp 等字段，映射到 Go 语言的 Subject、Role、ExpiresAt 变量里
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, errors.New("invalid token claims")
	}
	if time.Now().Unix() >= claims.ExpiresAt {
		return nil, errors.New("token expired")
	}
	if claims.Subject == "" || (claims.Role != RoleAdmin && claims.Role != RoleViewer) {
		return nil, errors.New("invalid token content")
	}
	return &claims, nil
}

// BearerToken 从请求中提取 token：优先 Authorization 头，其次 ?token= 查询参数（便于 EventSource 使用）。
func BearerToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	}
	return r.URL.Query().Get("token")
}
