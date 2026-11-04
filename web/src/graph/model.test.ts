import { test } from 'node:test'
import assert from 'node:assert/strict'
import { buildView, nodeRadius, carryPositions, RADIUS_MIN, RADIUS_MAX } from './model.ts'
import type { GraphPayload, ViewOptions } from './model.ts'

const g: GraphPayload = {
  notes: [
    { id: 'a', space: 'main', path: 'main/zk/a.md', title: 'Alpha' },
    { id: 'b', space: 'main', path: 'main/zk/b.md', title: 'Beta' },
    { id: 'c', space: 'main', path: 'main/zk/c.md', title: 'Gamma' },
    { id: 'd', space: 'main', path: 'main/zk/d.md', title: 'Delta' },
    { id: 'e', space: 'main', path: 'main/inbox/e.md', title: '' },
    { id: 'f', space: 'main', path: 'main/img/Diagram.excalidraw.md', title: '' },
  ],
  links: [[0, 1], [1, 2], [0, 5]],
  ghosts: [{ space: 'main', target: 'Missing', from: [0, 4] }],
  tags: [{ space: 'main', tag: 'x', notes: [0, 3] }],
}
const opts: ViewOptions = { search: '', orphans: true, ghosts: false, tags: false, colorBy: 'none' }
const view = (o: Partial<ViewOptions> = {}) => buildView(g, { ...opts, ...o })
const keys = (o: Partial<ViewOptions> = {}) => view(o).nodes.map((n) => n.key).sort()

test('default shows every note and link', () => {
  const v = view()
  assert.equal(v.nodes.length, 6)
  assert.ok(v.nodes.every((n) => n.kind === 'note'))
  assert.equal(v.links.length, 6)
})

test('search keeps matches and edges among them', () => {
  assert.deepEqual(keys({ search: 'ALP' }), ['a'])
  const v = view({ search: 'zk/' })
  assert.deepEqual(v.nodes.map((n) => n.key).sort(), ['a', 'b', 'c', 'd'])
  assert.equal(v.links.length, 4)
})

test('search with no match is empty', () => {
  assert.equal(view({ search: 'zzz' }).nodes.length, 0)
})

test('orphans off drops notes without edges', () => {
  assert.deepEqual(keys({ orphans: false }), ['a', 'b', 'c', 'f'])
})

test('ghosts on adds one shared ghost', () => {
  const n = view({ ghosts: true }).nodes.find((x) => x.key === 'ghost:main:missing')
  assert.ok(n)
  assert.equal(n.kind, 'ghost')
  assert.equal(n.label, 'Missing')
  assert.equal(n.degree, 2)
})

test('tags on adds tag nodes', () => {
  const n = view({ tags: true }).nodes.find((x) => x.key === 'tag:main:x')
  assert.ok(n)
  assert.equal(n.label, '#x')
  assert.equal(n.degree, 2)
  assert.ok(keys({ tags: true, orphans: false }).includes('d'))
})

test('local depth walks both directions', () => {
  assert.deepEqual(keys({ center: 'b' }), ['a', 'b', 'c'])
  assert.deepEqual(keys({ center: 'b', depth: 2 }), ['a', 'b', 'c', 'f'])
})

test('local search keeps the center', () => {
  assert.deepEqual(keys({ center: 'b', search: 'gamma' }), ['b', 'c'])
})

test('unknown center is empty', () => {
  assert.equal(view({ center: 'nope' }).nodes.length, 0)
})

test('label falls back to the file name', () => {
  const v = view()
  assert.equal(v.nodes.find((n) => n.key === 'e')?.label, 'e')
  assert.equal(v.nodes.find((n) => n.key === 'f')?.label, 'Diagram')
})

test('colorBy folder groups by top folder', () => {
  const v = view({ colorBy: 'folder' })
  assert.equal(v.nodes.find((n) => n.key === 'a')?.group, 'main/zk')
  assert.equal(v.nodes.find((n) => n.key === 'e')?.group, 'main/inbox')
})

test('nodeRadius is monotone and capped', () => {
  assert.equal(nodeRadius(0), RADIUS_MIN)
  assert.ok(nodeRadius(4) < nodeRadius(9))
  assert.equal(nodeRadius(10000), RADIUS_MAX)
})

test('carryPositions keeps known keys', () => {
  const out = carryPositions(['a', 'b'], new Float32Array([1, 2, 3, 4]), ['b', 'z'])
  assert.deepEqual(Array.from(out), [3, 4, NaN, NaN])
})
