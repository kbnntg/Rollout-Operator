import { defineStore } from 'pinia'
import { api, TOKEN_KEY } from '@/api/client'
import type { LoginResponse, UserInfo } from '@/api/types'

interface AuthState {
    token: string
    user: UserInfo | null
    loading: boolean
}

export const useAuthStore = defineStore('auth', {
    state: (): AuthState => ({
        token: localStorage.getItem(TOKEN_KEY) || '',
        user: null,
        loading: false,
    }),
    getters: {
        isAuthenticated: (state) => !!state.token,
        isAdmin: (state) => state.user?.role === 'admin',
    },
    actions: {
        async login(username: string, password: string): Promise<UserInfo> {
            this.loading = true
            try {
                const { data } = await api.post<LoginResponse>('/login', { username, password })
                this.token = data.token
                this.user = data.user
                localStorage.setItem(TOKEN_KEY, data.token)
                return data.user
            } finally {
                this.loading = false
            }
        },
        async fetchMe(): Promise<UserInfo | null> {
            if (!this.token) {
                return null
            }
            const { data } = await api.get<UserInfo>('/me')
            this.user = data
            return data
        },
        logout() {
            this.token = ''
            this.user = null
            localStorage.removeItem(TOKEN_KEY)
        },
    },
})