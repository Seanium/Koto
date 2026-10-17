import assert from 'node:assert/strict'
import { clampPane, paneBounds } from '../src/ui/paneSize.ts'

for (const window of [1120, 1280, 1440, 1920, 2560]) {
  for (const requestedSide of [-100, 190, 300, 2000]) {
    const side = clampPane(requestedSide, paneBounds(window - 1, 180, 300, 841))
    assert.ok(side >= 180 && side <= 300)
    for (const requestedList of [-100, 300, 460, 2000]) {
      const list = clampPane(requestedList, paneBounds(window - side - 2, 240, 460, 600))
      const content = window - side - list - 2
      assert.ok(list >= 240 && list <= 460)
      assert.ok(content >= 600, 'preserve room for toolbar and content')
      const bounds = paneBounds(content - 1, 220, Infinity, 280, .25, .75)
      for (const requestedTurns of [-100, 300, 2000]) {
        const turns = clampPane(requestedTurns, bounds)
        assert.ok(turns >= 220)
        assert.ok(content - 1 - turns >= 279.5)
      }
    }
  }
}
assert.equal(clampPane(300, paneBounds(0, 180, 300, 840)), 0, 'unmeasured containers do not overflow')
console.log('Pane constraints: all three dividers at minimum and large window sizes passed')
