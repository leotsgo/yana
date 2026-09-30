// Cross-language harness for the Phase 0 spike.
//
//   node fixtures.mjs encode <out.bin>            write a JS-authored update
//   node fixtures.mjs apply <in.bin>...           apply Go-authored updates in order, print text
//   node fixtures.mjs roundtrip <in.bin> <out.bin> apply a Go update, edit, write the delta back
//   node fixtures.mjs position <in.bin> <i> [assoc] print the relative position at index i as JSON
//   node fixtures.mjs resolve <in.bin> <pos.json> resolve a JSON relative position, print the index
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
} else if (cmd === 'position') {
  const doc = new Y.Doc()
  Y.applyUpdate(doc, new Uint8Array(readFileSync(args[0])))
  const rp = Y.createRelativePositionFromTypeIndex(doc.getText('body'), Number(args[1]), Number(args[2] ?? 0))
  process.stdout.write(JSON.stringify(Y.relativePositionToJSON(rp)))
} else if (cmd === 'resolve') {
  const doc = new Y.Doc()
  Y.applyUpdate(doc, new Uint8Array(readFileSync(args[0])))
  const pos = Y.createAbsolutePositionFromRelativePosition(Y.createRelativePositionFromJSON(JSON.parse(readFileSync(args[1], 'utf8'))), doc)
  process.stdout.write(String(pos == null ? -1 : pos.index))
} else {
  console.error('usage: fixtures.mjs encode|apply|roundtrip|position|resolve ...')
  process.exit(2)
}
