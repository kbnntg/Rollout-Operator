import { onBeforeUnmount, onMounted, ref } from 'vue'
import { TOKEN_KEY } from '@/api/client'

export interface RolloutEventPayload {
    type: string
    object: {
        metadata?: { name?: string; namespace?: string }
        status?: { phase?: string }
        [key: string]: unknown
    }
}

/**
 * useLiveEvents 订阅 /api/events（SSE），收到 Rollout 事件时回调。
 * 断线后自动重连；页面卸载时自动关闭连接。
 */
export function useLiveEvents(onRollout: (payload: RolloutEventPayload) => void) {
    const connected = ref(false)
    let source: EventSource | null = null
    let retryTimer: ReturnType<typeof setTimeout> | null = null
    let disposed = false

    function connect() {
        const token = localStorage.getItem(TOKEN_KEY)
        if (!token || disposed) {
            return
        }
        // EventSource 不能自定义请求头，所以 token 走 query（后端 BearerToken 支持）
        source = new EventSource(`/api/events?token=${encodeURIComponent(token)}`)
        source.onopen = () => {
            connected.value = true
        }
        source.addEventListener('rollout', (event) => {
            const message = event as MessageEvent<string>
            try {
                onRollout(JSON.parse(message.data) as RolloutEventPayload)
            } catch {
                // 忽略无法解析的事件
            }
        })
        source.onerror = () => {
            connected.value = false
            source?.close()
            source = null
            if (!disposed) {
                retryTimer = setTimeout(connect, 3000)
            }
        }
    }

    onMounted(() => {
        disposed = false
        connect()
    })

    onBeforeUnmount(() => {
        disposed = true
        if (retryTimer) {
            clearTimeout(retryTimer)
        }
        source?.close()
    })

    return { connected }
}