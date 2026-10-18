import assert from 'node:assert/strict'
import { updateErrorMessage } from '../src/api/updateErrors.ts'
import { setLanguage, type Locale } from '../src/i18n/core.ts'

for (const language of ['zh', 'zh-TW', 'en', 'ja', 'de'] as Locale[]) {
  setLanguage(language)
  const rate = updateErrorMessage({ error: 'raw HTTP 403', errorCode: 'RATE_LIMITED', errorStage: 'check', retryAt: Date.UTC(2026, 9, 18, 12, 30) })
  const forbidden = updateErrorMessage({ error: 'raw HTTP 403', errorCode: 'HTTP_FORBIDDEN', errorStage: 'check' })
  assert.ok(!rate.includes('GitHub') && !forbidden.includes('GitHub'), 'the update service can have a different origin')
  const unavailable = updateErrorMessage({ error: 'HTTP 503', errorCode: 'HTTP_UNAVAILABLE', errorStage: 'check' })
  assert.ok(!unavailable.includes('HTTP 503') && unavailable !== rate && unavailable !== forbidden)
  const stale = updateErrorMessage({ error: 'stale cache', errorCode: 'STALE_FEED', errorStage: 'check' })
  assert.ok(!stale.includes('stale cache') && stale !== rate && stale !== forbidden)
  if (language !== 'zh') assert.ok(!stale.includes('暂时'))
  assert.ok(!rate.includes('raw HTTP') && !forbidden.includes('raw HTTP'))
  assert.notEqual(rate, forbidden, 'ordinary refusal must not be called a rate limit')
  assert.notEqual(updateErrorMessage({ error: 'offline', errorStage: 'check' }), updateErrorMessage({ error: 'offline', errorStage: 'install' }))
  if (language !== 'zh') assert.ok(!rate.includes('暂时'))
}
setLanguage('zh')
assert.match(updateErrorMessage({ error: 'offline', errorStage: 'check' }), /^暂时无法检查更新：offline$/)
assert.match(updateErrorMessage({ error: 'offline', errorStage: 'install' }), /^更新失败：offline$/)
assert.equal(updateErrorMessage({ error: 'stale cache', errorCode: 'STALE_FEED' }), '暂时无法确认最新版本，请稍后重试或手动下载。')
assert.match(updateErrorMessage({ error: '', errorCode: 'RATE_LIMITED', retryAt: NaN }), /稍后重试/)
console.log('Update failures distinguish checks, installation, rate limits and refusal in all five languages')
