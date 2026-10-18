import { getLocale, tr } from '../i18n/core.ts'

export function updateErrorMessage(state: { error: string; errorCode?: string; errorStage?: string; retryAt?: number }) {
  if (state.errorCode === 'HTTP_UNAVAILABLE') return tr('更新服务暂时不可用，请稍后重试或手动下载。')
  if (state.errorCode === 'STALE_FEED') return tr('暂时无法确认最新版本，请稍后重试或手动下载。')
  if (state.errorCode === 'RATE_LIMITED') {
    if (state.retryAt && Number.isFinite(state.retryAt) && !Number.isNaN(new Date(state.retryAt).getTime())) {
      const time = new Date(state.retryAt).toLocaleString(getLocale(), { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
      return tr('更新服务暂时限制了请求。请在 {0} 后重试，或手动下载。', [time])
    }
    return tr('更新服务暂时限制了请求。请稍后重试，或手动下载。')
  }
  if (state.errorCode === 'HTTP_FORBIDDEN') return tr('更新服务拒绝了请求。请稍后重试，或手动下载。')
  return state.errorStage === 'check' ? tr('暂时无法检查更新：{0}', [state.error]) : tr('更新失败：{0}', [state.error])
}
