import { createRouter, createWebHistory } from 'vue-router'
import { TOKEN_KEY } from '@/api/client'

const router = createRouter({
    history: createWebHistory(),
    routes: [
        {
            path: '/login',
            name: 'login',
            component: () => import('@/views/LoginView.vue'),
            meta: { public: true, title: '登录' },
        },
        { path: '/', redirect: '/rollouts' },
        {
            path: '/rollouts',
            name: 'rollouts',
            component: () => import('@/views/RolloutsView.vue'),
            meta: { title: '发布看板' },
        },
        {
            path: '/rollouts/:namespace/:name',
            name: 'rollout-detail',
            component: () => import('@/views/RolloutDetailView.vue'),
            props: true,
            meta: { title: '发布详情' },
        },
        {
            path: '/audit',
            name: 'audit',
            component: () => import('@/views/AuditView.vue'),
            meta: { title: '审计日志' },
        },
        { path: '/:pathMatch(.*)*', redirect: '/rollouts' },
    ],
})

// 未登录 → 跳登录页（带 redirect 回来）
router.beforeEach((to) => {
    const token = localStorage.getItem(TOKEN_KEY)
    if (!to.meta.public && !token) {
        return { path: '/login', query: { redirect: to.fullPath } }
    }
    if (to.path === '/login' && token) {
        return { path: '/rollouts' }
    }
    return true
})

router.afterEach((to) => {
    const title = (to.meta.title as string | undefined) ?? 'Rollout Console'
    document.title = `${title} | Rollout Console`
})

export default router