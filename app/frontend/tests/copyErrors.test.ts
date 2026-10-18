import assert from 'node:assert/strict'
import { copyBlocker, projectFolderError } from '../src/api/copyErrors.ts'
import { setLanguage, type Locale } from '../src/i18n/core.ts'

const missing = "cwd /a project/中文 does not exist; choose an existing project folder"
const routine = '2 harness context events (target system prompt/environment) are not migrated'
setLanguage('zh')
assert.deepEqual(copyBlocker([routine, missing]), {
  message: '原项目目录已不存在，请选择一个存在的目录。',
})
assert.deepEqual(copyBlocker([missing, routine]), copyBlocker([routine, missing]))
assert.equal(projectFolderError('attachment /missing/image.png does not exist'), undefined)
assert.equal(projectFolderError('database is locked'), undefined)
assert.equal(copyBlocker([routine, 'target is not writable']).detail, routine + '; target is not writable')
assert.match(projectFolderError('project path is not a directory')!, /不是目录/)
for (const language of ['zh', 'zh-TW', 'en', 'ja', 'de'] as Locale[]) {
  setLanguage(language)
  const result = copyBlocker([routine, missing])
  assert.ok(result.message.length > 0)
  assert.equal(result.detail, undefined)
  assert.ok(!result.message.includes('harness') && !result.message.includes('cwd '))
  if (language !== 'zh') assert.notEqual(result.message, '原项目目录已不存在，请选择一个存在的目录。')
}
console.log('Explicit project blockers are visible and localized; routine notes cannot obscure them')
