import { tr } from '../i18n/core.ts'
import { useState } from 'react'
import { shell, useOpeners } from '../api/client'
import { Icon, Menu, Toast } from '../ui'

/** Continuation actions belong to the opened conversation. */
export function ResumeActions({ command, subject = tr("当前会话") }: { command: string; subject?: string }) {
  const openers = useOpeners()
  const [note, setNote] = useState('')
  const copy = async () => setNote(await shell.copy(command).catch(() => false) ? tr("续聊命令已复制") : tr("复制续聊命令失败，请重试"))
  const terminal = async () => {
    const r = await shell.openInTerminal(command).catch(() => '')
    setNote(r === 'ok' ? tr("已在终端打开") : r === 'copied' ? tr("命令已复制，请粘贴到终端运行") : tr("打开终端失败，请复制命令运行"))
  }
  return <>
    <Menu trigger="secondary" label={tr("继续当前会话")} width={260} items={[
      { label: tr("复制续聊命令"), title: tr("{0}的续聊命令：{1}", [subject, command]), onClick: () => void copy() },
      ...(openers?.info.terminal ? [{ label: tr("在终端中续聊"), onClick: () => void terminal() }] : []),
    ]}>{Icon.terminal}{Icon.chevron}</Menu>
    {note && <Toast text={note} onDone={() => setNote('')} />}
  </>
}
