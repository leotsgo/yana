import assert from 'node:assert/strict'
import { test } from 'node:test'

import { keepSame } from './keep-same.ts'

const tree = () => [
  { name: 'zk', notes: 3, children: [
    { type: 'note', path: 'zk/a.md', title: 'A', tags: ['x'] },
    { type: 'note', path: 'zk/b.md', title: 'B' },
    { type: 'dir', path: 'zk/d', children: [{ type: 'note', path: 'zk/d/c.md', title: 'C' }] },
  ] },
]

test('an equal tree comes back as the previous one', () => {
  const prev = tree()
  assert.equal(keepSame(prev, tree()), prev)
})

test('only the changed path gets new objects', () => {
  const prev = tree()
  const next = tree()
  next[0]!.children[1]!.title = 'B2'
  const out = keepSame(prev, next)
  assert.notEqual(out, prev)
  assert.deepEqual(out, next)
  assert.equal(out[0]!.children[0], prev[0]!.children[0])
  assert.equal(out[0]!.children[2], prev[0]!.children[2])
  assert.notEqual(out[0]!.children[1], prev[0]!.children[1])
})

test('an insert keeps the siblings after it, matched by path', () => {
  const prev = tree()
  const next = tree()
  next[0]!.children.unshift({ type: 'note', path: 'zk/0.md', title: '0' })
  const out = keepSame(prev, next)
  assert.deepEqual(out, next)
  assert.equal(out[0]!.children[1], prev[0]!.children[0])
  assert.equal(out[0]!.children[3], prev[0]!.children[2])
})

test('a removed field or tag is a change', () => {
  const prev = tree()
  const next = tree()
  delete (next[0]!.children[0] as { tags?: string[] }).tags
  assert.notEqual(keepSame(prev, next)[0]!.children[0], prev[0]!.children[0])
  const next2 = tree()
  next2[0]!.children[0]!.tags = ['y']
  assert.deepEqual(keepSame(prev, next2), next2)
  assert.equal(keepSame(null, next2), next2)
})
