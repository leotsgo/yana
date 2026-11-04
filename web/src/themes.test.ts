import assert from 'node:assert/strict'
import { test } from 'node:test'
import { BUILTIN, TOKENS, parseTheme, themeVars } from './themes.ts'

test('a pasted theme needs a name, a mode and hex colours', () => {
  const ok = parseTheme('{"name":"Forest","mode":"dark","colors":{"bg":"#14201a","ink":"#d8e6d0","accent":"#e0b24a"}}')
  assert.equal(typeof ok, 'object')
  assert.equal((ok as { id: string }).id, 'custom:forest')
  assert.equal(typeof parseTheme('nope'), 'string')
  assert.equal(typeof parseTheme('{"name":"x","mode":"dim","colors":{}}'), 'string')
  assert.equal(typeof parseTheme('{"name":"x","mode":"dark","colors":{"bg":"red","ink":"#fff","accent":"#000"}}'), 'string')
  assert.equal(typeof parseTheme('{"name":"x","mode":"dark","colors":{"bg":"#000","ink":"#fff","accent":"#000","x":"#000"}}'), 'string')
  assert.equal(typeof parseTheme('{"name":"x","mode":"dark","colors":{"bg":"#000"}}'), 'string')
})

test('three colours fill every token; built-ins are complete', () => {
  const v = themeVars({ id: 'c', name: 'c', mode: 'light', colors: { bg: '#ffffff', ink: '#000000', accent: '#ff0000' } })
  for (const k of TOKENS) assert.ok(v[k], k)
  assert.equal(v['bg-3'], '#ebebeb')
  for (const d of BUILTIN) if (d.id.startsWith('yana')) assert.deepEqual(themeVars(d), {})
  else for (const k of TOKENS) assert.match(themeVars(d)[k] ?? '', /^#[0-9a-f]{6}$/i, `${d.id} ${k}`)
})
