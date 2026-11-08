/** 从 axios 错误或未知异常中提取可读的错误信息。 */
export function errorMessage(err: unknown, fallback = '请求失败'): string {
    if (typeof err === 'object' && err !== null) {
        const candidate = err as {
            response?: { data?: { error?: string }; status?: number }
            message?: string
        }
        return candidate.response?.data?.error || candidate.message || fallback
    }
    return fallback
}

/** 将 RFC3339 时间格式化为本地时间。 */
export function formatTime(iso: string): string {
    if (!iso) {
        return '-'
    }
    const date = new Date(iso)
    if (Number.isNaN(date.getTime())) {
        return iso
    }
    return date.toLocaleString('zh-CN', { hour12: false })
}

/** 缩短模板哈希，便于展示。 */
export function shortHash(hash: string | undefined): string {
    if (!hash) {
        return '—'
    }
    return hash.length > 12 ? `${hash.slice(0, 12)}…` : hash
}

export function actionLabel(action: string): string {
    const map: Record<string, string> = {
        promote: '立即发布',
        abort: '中止回滚',
        pause: '暂停发布',
        resume: '恢复发布',
        retry: '重试发布',
    }
    return map[action] ?? action
}