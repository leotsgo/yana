import assert from 'node:assert/strict'
import { test } from 'node:test'

import { hostOf, splitError } from './setuplib.ts'

test('hostOf reads scp-like and ssh:// addresses', () => {
  assert.equal(hostOf('git@github.com:leotsgo/notes.git'), 'github.com')
  assert.equal(hostOf('github.com:notes.git'), 'github.com')
  assert.equal(hostOf('ssh://git@git.example.org:2222/srv/notes.git'), 'git.example.org')
  assert.equal(hostOf('ssh://host/notes.git'), 'host')
  assert.equal(hostOf('ssh://git@[::1]:22/notes.git'), '[::1]')
  assert.equal(hostOf('/srv/backups/notes.git'), '/srv/backups/notes.git')
})

test('splitError keeps the first line apart from git output', () => {
  assert.deepEqual(splitError('refused the key.\ngit@x: Permission denied.\n'), { head: 'refused the key.', detail: 'git@x: Permission denied.' })
  assert.deepEqual(splitError('plain'), { head: 'plain', detail: '' })
})
