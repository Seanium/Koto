import assert from 'node:assert/strict'
import { lossSentences } from '../src/api/model.ts'
import { setLanguage } from '../src/i18n/core.ts'
import type { Loss } from '../src/api/types.ts'
setLanguage('zh')
const loss = (field: string, kind: Loss['kind'], note?: string): Loss => ({ sink: 'test', event: 'e', field, kind, note })
assert.deepEqual(lossSentences([loss('tool_result','textualized')]), ['为兼容目标格式，工具调用和结果以文字保留'])
assert.deepEqual(lossSentences([loss('tool_result','textualized','orphan result (no matching call)')]), ['未找到对应调用，工具结果以文字保留'])
assert.deepEqual(lossSentences([loss('tool_result','dropped','result of a skipped assistant turn')]), ['部分工具记录未复制'])
assert.deepEqual(lossSentences([loss('history','approximated'),loss('message','approximated')]), ['较早内容只含摘要','部分对话已压缩为摘要'])
assert.deepEqual(lossSentences([loss('message','truncated')]), ['部分内容被截断'])
assert.deepEqual(lossSentences([loss('tool_result','textualized'),loss('tool_call:Read','textualized')]), ['为兼容目标格式，工具调用和结果以文字保留'])
console.log('Copy notes preserve distinct material losses and conversion reasons')

assert.deepEqual(lossSentences([loss('reasoning','dropped','empty reasoning')]), [])
assert.deepEqual(lossSentences([loss('reasoning','dropped','encrypted by anthropic')]), ['不含加密推理内容'])
assert.deepEqual(lossSentences([loss('reasoning','dropped')]), ['部分推理内容未复制'])
