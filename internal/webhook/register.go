package webhook

import ctrl "sigs.k8s.io/controller-runtime"

// Register 注册 Rollout 的准入 Webhook
func Register(mgr ctrl.Manager) {}
