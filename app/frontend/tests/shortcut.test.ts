import assert from 'node:assert/strict'
import { test } from 'node:test'
import { shortcut } from '../src/ui/shortcut.ts'

test('shortcut hints match Windows and Linux keys while macOS keeps its symbols', () => {
  for (const platform of ['Win32', 'Linux aarch64', 'Linux x86_64']) {
    assert.equal(shortcut('⌘K', platform), 'Ctrl+K')
    assert.equal(shortcut('⌘↵', platform), 'Ctrl+Enter')
    assert.equal(shortcut('⌥⌘R', platform), 'Ctrl+Alt+R')
    assert.equal(shortcut('Move to Trash (⌘⌫)', platform), 'Move to Trash (Ctrl+Backspace)')
    assert.equal(shortcut('前のメッセージ（⌘↑）', platform), '前のメッセージ（Ctrl+↑）')
  }
  for (const hint of ['⌘K', '⌘↵', '⌥⌘R', 'Move to Trash (⌘⌫)']) {
    assert.equal(shortcut(hint, 'MacIntel'), hint)
  }
})
