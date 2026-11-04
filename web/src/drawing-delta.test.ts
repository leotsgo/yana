import assert from 'node:assert/strict'
import { test } from 'node:test'

import { changedSince, markSent, pack, packFile } from './drawing-delta.ts'
import type { Delta } from './drawing-delta.ts'

const el = (id: string, version: number, extra = '') => ({ id, version, extra })

test('only elements whose version moved are sent, once', () => {
  const sent = new Map<string, number>()
  assert.deepEqual(changedSince([el('a', 1), el('b', 1)], sent).map((e) => e.id), ['a', 'b'])
  assert.deepEqual(changedSince([el('a', 1), el('b', 1)], sent), [])
  assert.deepEqual(changedSince([el('a', 2), el('b', 1)], sent).map((e) => e.id), ['a'])
})

test('a deletion is a new version and goes out like any edit', () => {
  const sent = new Map<string, number>([['a', 3]])
  const gone = { id: 'a', version: 4, isDeleted: true }
  assert.deepEqual(changedSince([gone], sent), [gone])
})

test('what came from the room is not sent back, unless edited since', () => {
  const sent = new Map<string, number>()
  markSent([el('r', 5)], sent)
  assert.deepEqual(changedSince([el('r', 5)], sent), [])
  // A remote element that lost to a newer local edit still goes out.
  markSent([el('r', 4)], sent)
  assert.deepEqual(changedSince([el('r', 6)], sent).map((e) => e.version), [6])
})

test('pack splits into frames under the limit and keeps every element once', () => {
  const els = Array.from({ length: 50 }, (_, i) => el('e' + i, 1, 'x'.repeat(100)))
  const frames = pack(els, 1000, true)
  assert.ok(frames.length > 5)
  const seen: string[] = []
  frames.forEach((f, i) => {
    assert.ok(new TextEncoder().encode(f).length <= 1000, `frame ${i} is ${f.length} bytes`)
    const d = JSON.parse(f) as Delta
    assert.equal(d.hello, i === 0 ? true : undefined)
    for (const e of d.els ?? []) seen.push((e as { id: string }).id)
  })
  assert.deepEqual(seen, els.map((e) => e.id))
})

test('an element too large for a frame is left out; hello still goes', () => {
  const frames = pack([el('big', 1, 'x'.repeat(5000))], 1000, true)
  assert.deepEqual(frames.map((f) => JSON.parse(f) as Delta), [{ hello: true, els: [] }])
  assert.deepEqual(pack([], 1000), [])
})

test('a file goes alone, and only when it fits', () => {
  assert.deepEqual(JSON.parse(packFile({ id: 'f', dataURL: 'data:,x' }, 1000) ?? 'null'), { files: [{ id: 'f', dataURL: 'data:,x' }] })
  assert.equal(packFile({ id: 'f', dataURL: 'x'.repeat(2000) }, 1000), null)
})
