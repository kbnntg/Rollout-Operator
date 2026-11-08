<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const isPublic = computed(() => route.meta.public === true || route.path === '/login')

const navItems = [
  { to: '/rollouts', label: '发布看板', icon: '◈' },
  { to: '/audit', label: '审计日志', icon: '☰' },
]

onMounted(async () => {
  // 刷新页面后只有 token、没有 user 时，用 /api/me 补回来
  if (auth.token && !auth.user) {
    try {
      await auth.fetchMe()
    } catch {
      auth.logout()
      router.push('/login')
    }
  }
})

function logout() {
  auth.logout()
  router.push('/login')
}
</script>

<template>
  <div v-if="isPublic" class="auth-layout">
    <router-view />
  </div>

  <div v-else class="shell">
    <aside class="sidebar">
      <div class="brand">
        <span class="logo">◈</span>
        <div class="brand-text">
          <strong>Rollout Console</strong>
          <small>金丝雀发布控制台</small>
        </div>
      </div>

      <nav class="nav">
        <router-link
          v-for="item in navItems"
          :key="item.to"
          :to="item.to"
          class="nav-item"
          active-class="active"
        >
          <span class="nav-icon">{{ item.icon }}</span>
          <span>{{ item.label }}</span>
        </router-link>
      </nav>

      <div class="sidebar-foot">
        <div class="user">
          <span class="avatar">{{ auth.user?.name?.slice(0, 1).toUpperCase() || '?' }}</span>
          <div class="user-meta">
            <strong>{{ auth.user?.name || '未登录' }}</strong>
            <small>{{ auth.user?.role === 'admin' ? '管理员' : '只读账号' }}</small>
          </div>
        </div>
        <button class="btn ghost small" type="button" @click="logout">退出</button>
      </div>
    </aside>

    <main class="main">
      <router-view :key="route.fullPath" />
    </main>
  </div>
</template>