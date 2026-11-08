import axios from 'axios'

/** localStorage 中保存 JWT 的键名。 */
export const TOKEN_KEY = 'rollout-console-token'

export const api = axios.create({
    baseURL: '/api',
    timeout: 15000,
})

// 请求拦截：自动带上 JWT
api.interceptors.request.use((config) => {
    const token = localStorage.getItem(TOKEN_KEY)
    if (token) {
        config.headers.Authorization = `Bearer ${token}`
    }
    return config
})

// 响应拦截：401 时清 token 并跳登录
api.interceptors.response.use(
    (response) => response,
    (error) => {
        if (error?.response?.status === 401) {
            localStorage.removeItem(TOKEN_KEY)
            if (window.location.pathname !== '/login') {
                window.location.href = '/login'
            }
        }
        return Promise.reject(error)
    },
)