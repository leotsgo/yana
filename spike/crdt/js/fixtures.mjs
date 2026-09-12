// Cross-language harness for the Phase 0 spike.
//
//   node fixtures.mjs encode <out.bin>            write a JS-authored update
//   node fixtures.mjs apply <in.bin>...           apply Go-authored updates in order, print text
//   node fixtures.mjs roundtrip <in.bin> <out.bin> apply a Go update, edit, write the delta back
import * as Y from 'yjs'
import { readFileSync, writeFileSync } from 'node:fs'

const [, , cmd, ...args] = process.argv

if (cmd === 'encode') {
  const doc = new Y.Doc()
  const text = doc.getText('body')
  doc.transact(() => {
    text.insert(0, '# Title\n\nHello from JS 🙂 with ε and [[wikilink]].\n')
  })
  doc.transact(() => {
    text.delete(9, 5) // remove "Hello"
    text.insert(9, 'Bonjour')
  })
  writeFileSync(args[0], Y.encodeStateAsUpdate(doc))
  process.stdout.write(text.toString())
} else if (cmd === 'apply') {
  const doc = new Y.Doc()
  for (const f of args) Y.applyUpdate(doc, new Uint8Array(readFileSync(f)))
  process.stdout.write(doc.getText('body').toString())
} else if (cmd === 'roundtrip') {
  const doc = new Y.Doc()
  Y.applyUpdate(doc, new Uint8Array(readFileSync(args[0])))
  const before = Y.encodeStateVector(doc)
  const text = doc.getText('body')
  doc.transact(() => { text.insert(text.length, ' +js') })
  writeFileSync(args[1], Y.encodeStateAsUpdate(doc, before))
  process.stdout.write(text.toString())
} else {
  console.error('usage: fixtures.mjs encode|apply|roundtrip ...')
  process.exit(2)
}
