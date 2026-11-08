import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

export default defineConfig({
    plugins: [vue()],
    resolve: {
        alias: {
            '@': fileURLToPath(new URL('./src', import.meta.url)),
        },
    },
    server: {
        host: '0.0.0.0',   // 方便从宿主机浏览器访问
        port: 5173,
        proxy: {
            '/api': {
                target: 'http://127.0.0.1:8090',   // 开发时把 /api 代理到本地控制台后端
                changeOrigin: true,
            },
        },
    },
    build: {
        outDir: '../cmd/controlplane/webdist',  // 构建产物直接落进 go:embed 目录
        emptyOutDir: true,
        sourcemap: false,
    },
})