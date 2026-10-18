import assert from 'node:assert/strict'
import { catalogs, getLanguage, getLocale, languages, resolveLocale, setLanguage, subscribeLanguage, tr } from '../src/i18n/core.ts'

for (const [system, expected] of [['zh-CN', 'zh'], ['zh-Hans-US', 'zh'], ['zh-Hans-SG', 'zh'], ['zh-Hant', 'zh-TW'], ['zh-Hant-HK', 'zh-TW'], ['zh_TW', 'zh-TW'], ['zh-MO', 'zh-TW'], ['ja-JP', 'ja'], ['de-AT', 'de'], ['fr-FR', 'en']]) {
  assert.equal(resolveLocale('system', system), expected)
  assert.equal(resolveLocale('en', system), 'en', 'explicit preference overrides the system')
}
const keys = Object.keys(catalogs.en).sort(), slots = (text: string) => (text.match(/\{[^{}]+\}/g) ?? []).sort()
for (const [locale, catalog] of Object.entries(catalogs)) {
  assert.deepEqual(Object.keys(catalog).sort(), keys, `${locale} has every message`)
  for (const key of keys) assert.deepEqual(slots(catalog[key]), slots(key), `${locale}: ${key}`)
}
let notifications = 0
const unsubscribe = subscribeLanguage(() => notifications++)
setLanguage('ja')
assert.equal(tr('第 {0} 轮', [4]), 'ターン 4')
assert.equal(tr('项目 {0}', ['正文不翻译']), 'プロジェクト 正文不翻译')
setLanguage('de')
const before = notifications
setLanguage('de')
assert.equal(notifications, before, 'unchanged locale does not render again')
assert.equal(tr('{path} 是项目文件夹'), '{path} ist der Projektordner')
assert.equal(tr('unknown label'), 'unknown label')
unsubscribe()
setLanguage('zh-TW')
assert.equal(notifications, before, 'unsubscribed listener is released')
assert.equal(getLocale(), 'zh-TW')
setLanguage('invalid')
assert.equal(getLanguage(), 'system')
assert.equal(languages.length, 6)
setLanguage('zh')
console.log('Locales: five catalogs, system detection, placeholders, content preservation and listener cleanup passed')
