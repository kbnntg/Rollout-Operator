<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { errorMessage } from '@/utils/format'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const username = ref('')
const password = ref('')
const error = ref('')
const submitting = ref(false)

async function submit() {
  error.value = ''
  if (!username.value.trim() || !password.value) {
    error.value = '请输入用户名和密码'
    return
  }
  submitting.value = true
  try {
    await auth.login(username.value.trim(), password.value)
    const redirect = (route.query.redirect as string) || '/rollouts'
    await router.push(redirect)
  } catch (err) {
    error.value = errorMessage(err, '登录失败，请检查用户名密码或后端服务')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="login-card">
    <div class="login-hero">
      <span class="logo">◈</span>
      <h1>Rollout Operator<br />渐进式发布控制台</h1>
      <p>
        基于 Kubernetes Operator 实现金丝雀发布：流量权重编排、Prometheus 指标门禁、
        异常自动回滚，全部操作可审计。
      </p>
      <ul>
        <li>多步骤流量切分与暂停观察</li>
        <li>PromQL 指标门禁，连续采样达标才放量</li>
        <li>指标恶化自动回滚，发布过程实时可见</li>
        <li>JWT 认证 + 管理员/只读角色分级</li>
      </ul>
    </div>

    <form class="login-form" @submit.prevent="submit">
      <h2>登录控制台</h2>
      <div v-if="error" class="error-banner">{{ error }}</div>

      <div class="field">
        <label for="username">用户名</label>
        <input id="username" v-model="username" autocomplete="username" placeholder="admin 或 viewer" />
      </div>
      <div class="field">
        <label for="password">密码</label>
        <input
          id="password"
          v-model="password"
          type="password"
          autocomplete="current-password"
          placeholder="请输入密码"
        />
      </div>

      <button class="btn wide" type="submit" :disabled="submitting">
        {{ submitting ? '登录中…' : '登录' }}
      </button>
      <p class="login-hint">
        默认演示账号：<code>admin / admin123</code>（管理员）、<code>viewer / viewer123</code>（只读）。
        生产环境请通过 Secret 注入用户与 JWT 密钥。
      </p>
    </form>
  </div>
</template>