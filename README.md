# Rollout Operator · Kubernetes 渐进式发布 Operator 与发布控制台

用 Go + controller-runtime 从零实现的声明式金丝雀发布平台：自定义 `Rollout` CRD 描述 Pod 模板与发布步骤，Operator 自动维护稳定/金丝雀两套工作负载、按步骤切流量、结合 Prometheus 指标自动判定成功或回滚；配套 Vue3 + TypeScript 可视化控制台（JWT 认证、RBAC 分级、SSE 实时刷新、操作审计）。

## 架构

```text
                ┌───────────────────────────────┐
                │        Kubernetes 集群        │
                │                               │
  Rollout CR ───┼──▶ controller-manager         │
     ▲          │      ├─ Deployment stable     │
     │ status   │      ├─ Deployment canary     │
     │          │      ├─ Service ×2            │
     │          │      └─ Ingress ×2 (nginx     │
     │          │            canary-weight)     │
     │          │      └─ PromQL ──▶ Prometheus │
     │          │                               │
  rollout-console ──REST/SSE──▶ 浏览器控制台     │
     └── go:embed 内嵌 Vue 前端（单二进制）      │
                └───────────────────────────────┘
```



## 核心能力

### Operator

- 自定义 CRD `rollouts.delivery.sre.dev`：Pod 模板 + 步骤序列 + 指标门禁
- `CreateOrUpdate` 幂等维护双版本 Deployment/Service/Ingress，OwnerReference + finalizer 管生命周期
- 模板哈希驱动发布：只有 `spec.template` 变化才开启新一轮金丝雀
- 状态机 `setWeight → pause → analysis → promote`，支持 promote/pause/resume/abort/retry 命令注解
- 失败阻塞：回滚后记录 `blockedTemplateHash`，不改模板或显式 retry 不会重推坏版本
- `dynamicStableScale`：发布期间按流量权重动态伸缩双版本副本

### 指标门禁

- analysis 步骤周期性执行 PromQL，连续 N 次达标才放量，连续失败自动回滚
- 查询异常同样按失败处理（可观测性失效时快速止损）
- 采样序列（每指标最多 60 点）写入 status 供前端绘图

### 控制台

- 发布看板 / 详情页（步骤时间线、权重环形仪表、ECharts 指标趋势、Pod 与 Deployment、状态条件）
- SSE 实时推送（client.WithWatch → EventSource），断线重连 + 轮询兜底
- JWT(HS256) 认证 + 管理员/只读 RBAC，后端路由层强制鉴权
- 变更操作二次确认并写审计日志（JSON Lines → ConfigMap），提供审计查询页
- 前端产物 go:embed 内嵌，单二进制部署

### 准入与安全

- Mutating Webhook：JSON Patch 注入默认值，保留未知字段
- Validating Webhook：步骤字段互斥、权重单调、pause 上限、analysis 指标引用校验
- distroless 非 root 镜像、只读根文件系统、最小权限 RBAC、leader election

## 目录结构

text

```tex
api/v1alpha1/          CRD 类型定义 + deepcopy
cmd/manager/           Operator 进程（controller + webhook + metrics）
cmd/controlplane/      控制台后端（REST + SSE + JWT + 审计 + 内嵌前端）
internal/controller/   Rollout 调谐器（状态机 / 流量 / 回滚）
internal/analysis/     Prometheus 查询与阈值判定
internal/webhook/      Mutating / Validating 准入逻辑
internal/controlplane/ 控制台后端实现
internal/metrics/      Operator 自定义指标
web/                   Vue3 + TypeScript 控制台
config/                kustomize：CRD / RBAC / manager / webhook / controlplane
hack/                  webhook 证书脚本、假 Prometheus、webhook 逻辑自测工具
docs/                  设计说明 / 演示脚本 / 简历要点
```



## 快速开始

前置：Go 1.23+、Docker、kubectl、Node 20+、openssl、已就绪的 Kubernetes 集群。

### 本地开发

bash

```
# 1) 安装 CRD
kubectl apply -k config/crd

# 2) 跑 Operator（本地调试关闭 webhook，避免证书依赖）
go run ./cmd/manager --leader-elect=false --enable-webhooks=false \
  --metrics-bind-address=:19080 --health-probe-bind-address=:19081

# 3) 跑控制台后端（另开终端）
go run ./cmd/controlplane --addr=:8090 --namespace=default \
  --users=admin:admin123:admin,viewer:viewer123:viewer \
  --jwt-secret=dev-local-secret-0123456789abcdef

# 4) 前端热更新（再开一个终端）
cd web && npm install && npm run dev   # http://127.0.0.1:5173
```



### 部署到集群

bash

```
docker build -t <your-registry>/rollout-operator:latest .
docker build -f Dockerfile.console -t <your-registry>/rollout-console:latest .
docker save <your-registry>/rollout-operator:latest | ctr -n k8s.io images import -
docker save <your-registry>/rollout-console:latest   | ctr -n k8s.io images import -

kubectl apply -k config/default && bash hack/gen-certs.sh
kubectl -n rollout-system get pods -w

kubectl -n rollout-system port-forward --address 0.0.0.0 svc/rollout-console 8090:80
# 浏览器打开 http://<节点IP>:8090   管理员 admin/admin123，只读 viewer/viewer123
```



## CRD 关键字段

| 字段                                           | 说明                                               |
| :--------------------------------------------- | :------------------------------------------------- |
| `spec.template`                                | Pod 模板，模板哈希变化触发新一轮发布               |
| `spec.replicas` / `spec.port` / `spec.ingress` | 基础工作负载与对外暴露                             |
| `spec.strategy.canary.steps`                   | 步骤序列：setWeight / pause / analysis             |
| `spec.strategy.canary.dynamicStableScale`      | 是否按权重动态伸缩双版本副本（默认 true）          |
| `spec.analysis`                                | Prometheus 地址、采样间隔、成功/失败阈值、指标列表 |
| `status.phase`                                 | Steady / Progressing / Paused / Failed / Aborted   |
| `status.blockedTemplateHash`                   | 失败模板哈希，用于阻止自动重试                     |

## 命令注解（等价于控制台按钮）

bash

```
kubectl -n <ns> annotate ro <name> delivery.sre.dev/command=promote --overwrite
kubectl -n <ns> annotate ro <name> delivery.sre.dev/command=pause   --overwrite
kubectl -n <ns> annotate ro <name> delivery.sre.dev/command=resume  --overwrite
kubectl -n <ns> annotate ro <name> delivery.sre.dev/command=abort   --overwrite
kubectl -n <ns> annotate ro <name> delivery.sre.dev/command=retry   --overwrite
```



## 控制台 API

| 方法 | 路径                                | 权限  | 说明                                      |
| :--- | :---------------------------------- | :---- | :---------------------------------------- |
| POST | `/api/login`                        | 公开  | 登录，返回 JWT                            |
| GET  | `/api/me`                           | 登录  | 当前用户                                  |
| GET  | `/api/rollouts`                     | 登录  | Rollout 列表（含指标最新值）              |
| GET  | `/api/rollouts/{ns}/{name}`         | 登录  | 详情（步骤/条件/Pod/Deployment/采样序列） |
| POST | `/api/rollouts/{ns}/{name}/actions` | admin | 下发命令                                  |
| GET  | `/api/audit`                        | 登录  | 审计日志                                  |
| GET  | `/api/events`                       | 登录  | SSE 实时事件                              |
| GET  | `/api/healthz` / `/metrics`         | 公开  | 健康检查 / 指标                           |

## 测试

bash

```
go vet ./...
go test ./... -count=1
go test ./internal/... -cover
```



当前：38 个用例全部通过，覆盖率 analysis 74.4% / controller 76.1% / controlplane 61.7% / webhook 59.0%。测试基于 controller-runtime fake client 与 httptest，覆盖发布推进、指标门禁通过/回滚、blocked 防重试、命令处理、finalizer、JWT 签发与篡改、只读账号越权、审计落库、SSE 中间件 Flusher 透传。

## 可观测性

- **Operator 指标**（`/metrics`，端口 8080）：`rollout_operator_rollouts_started_total`、`rollouts_promoted_total`、`rollouts_rolled_back_total{reason}`、`analysis_samples_total{metric,passed}`、`canary_weight{rollout}`。
- **控制台指标**（端口 8090）：`controlplane_http_requests_total`、`controlplane_http_request_duration_seconds`、`controlplane_rollout_actions_total`、`controlplane_audit_records_total`。

## 已知取舍

- promote 以“期望模板已写入 Deployment”为准，不等待 Pod 全部 Ready（Argo Rollouts 会等待可用副本达标）
- 流量切分依赖 nginx ingress 的 canary-weight 注解，未适配 Istio/其他网关
- BlueGreen 策略仅有字段占位，未实现切流
- 审计存 ConfigMap（零中间件依赖），数据量大时应替换为数据库
- 控制台单副本部署，未做多副本会话共享

## 开发工具

- `hack/fakeprom`：假 Prometheus（`VALUE=0.9 go run ./hack/fakeprom`），无需安装监控栈即可演示门禁
- `hack/webhookcheck`：直接调用 webhook 逻辑校验默认值与拒绝规则
- `hack/gen-certs.sh`：签发 webhook 自签证书并注入 caBundle

## 文档

- `docs/DESIGN.md` 设计说明与取舍
- `docs/DEMO.md` 演示脚本