import type { AnalysisNode } from '../api/types.ts'
import { tr } from '../i18n/core.ts'

const labelKeys: Record<string, string> = {
  user: '提问', assistant: '回复', tool: '工具', context: '上下文',
  reasoning: '思考', call: '调用 {0}', result: '结果（{0}）', attachment: '附件 {0}',
  truncated: '…（内容较长，请在会话中查看全文）',
}

/** Only backend-annotated labels are translated. Native text and comparison keys stay intact. */
export function analysisDisplayLines(node: AnalysisNode | undefined) {
  if (!node) return []
  const lines = node.text.split('\n')
  for (const label of node.labels ?? []) {
    const key = labelKeys[label.kind]
    if (key && Number.isInteger(label.line) && label.line >= 0 && label.line < lines.length) {
      lines[label.line] = tr(key, [label.value ?? ''])
    }
  }
  return lines
}
