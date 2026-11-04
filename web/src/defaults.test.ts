import assert from 'node:assert/strict'
import { test } from 'node:test'

import { dailyPath, pinned } from './defaults.ts'

test('a folder counts only while its space is there', () => {
  assert.equal(pinned('sb/5 zettelkasten', ['sb', 'work']), 'sb/5 zettelkasten')
  assert.equal(pinned('sb', ['sb']), 'sb')
  assert.equal(pinned('gone/x', ['sb']), '')
  assert.equal(pinned('', ['sb']), '')
})

test('the daily folder takes the place of the fixed folders, the dated part stays', () => {
  const p = 'journal/{YYYY}/{MM}/{YYYY}-{MM}-{DD}.md'
  assert.equal(dailyPath(p, '1 daily', '2026-11-04'), '1 daily/2026/11/2026-11-04.md')
  assert.equal(dailyPath(p, '', '2026-11-04'), '2026/11/2026-11-04.md')
  assert.equal(dailyPath('{date}.md', 'a/b', '2026-11-04'), 'a/b/2026-11-04.md')
  assert.equal(dailyPath('journal/today.md', 'd', '2026-11-04'), 'd/today.md')
  assert.equal(dailyPath(p, undefined, '2026-11-04'), 'journal/2026/11/2026-11-04.md')
})
