import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { SpaceTree } from './api.ts'
import { dailySeed, newID, numbered, offlineBlock, withLocal } from './localnote.ts'

test('ids have the shape of the server ULIDs and sort by time', () => {
  const id = newID()
  assert.match(id, /^[0-7][0-9A-HJKMNP-TV-Z]{25}$/)
  // The time part is the millisecond clock in Crockford base32.
  assert.equal(newID(0, new Uint8Array(16)), '00000000000000000000000000')
  assert.equal(newID(1_469_918_176_385, new Uint8Array(16).fill(31)).slice(0, 10), '01ARYZ6S41')
  assert.equal(newID(1, new Uint8Array(16).fill(255)), '0000000001ZZZZZZZZZZZZZZZZ')
  assert.ok(newID(1000) < newID(2000))
  assert.notEqual(newID(), newID())
})

test('local notes join the tree, folders made, the input untouched', () => {
  const tree: SpaceTree[] = [{ name: 'main', notes: 1, children: [{ type: 'note', name: 'a.md', path: 'main/a.md', id: 'A' }] }]
  const out = withLocal(tree, [
    { id: 'L1', path: 'main/journal/2026/11/2026-11-04.md' },
    { id: 'L2', path: 'main/b.md' },
    { id: 'A', path: 'main/a.md' }, // the server has it already
  ])
  assert.equal(tree[0]!.children.length, 1)
  const kids = out[0]!.children
  assert.deepEqual(kids.map((c) => c.path), ['main/a.md', 'main/journal', 'main/b.md'])
  const day = kids[1]!.children![0]!.children![0]!.children![0]!
  assert.deepEqual([day.id, day.path, day.title, day.local], ['L1', 'main/journal/2026/11/2026-11-04.md', '2026-11-04', true])
  assert.equal(kids[1]!.children![0]!.path, 'main/journal/2026')
  assert.equal(withLocal(tree, []), tree)
})

test('the offline text goes under a rule, without the seed heading', () => {
  const seed = dailySeed('2026-11-04')
  assert.equal(offlineBlock(seed + 'written on the bus\n', seed), '\n---\n\nwritten on the bus\n')
  assert.equal(offlineBlock(seed, seed), '')
  assert.equal(offlineBlock('', seed), '')
  assert.equal(offlineBlock('# Other\n\nx', seed), '\n---\n\n# Other\n\nx\n')
})

test('a taken name gets a number', () => {
  assert.equal(numbered('main/Untitled.md', 2), 'main/Untitled 2.md')
  assert.equal(numbered('main/Untitled 2.md', 3), 'main/Untitled 3.md')
  assert.equal(numbered('main/a b/2026-11-04.md', 2), 'main/a b/2026-11-04 2.md')
})
