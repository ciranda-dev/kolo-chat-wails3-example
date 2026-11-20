import test from 'node:test'
import assert from 'node:assert/strict'
import { pinnedNotes, pinProblem, shortKey } from './pins.js'

test('shortKey keeps the first 8 characters', () => {
  assert.equal(shortKey('00112233445566778899'), '00112233')
  assert.equal(shortKey(undefined), '')
})

test('pinProblem prefers the push, then the pull', () => {
  assert.equal(pinProblem({ push: 'down', pull: 'no pull' }), 'down')
  assert.equal(pinProblem({ push: '', pull: 'no pull' }), 'no pull')
  assert.equal(pinProblem({ push: '', pull: '' }), '')
  assert.equal(pinProblem(null), '')
})

test('pinnedNotes speaks of messages not opened yet, never of waiting for the sender', () => {
  assert.deepEqual(pinnedNotes(0), [])
  assert.deepEqual(pinnedNotes(1), ['1 message could not be opened yet. The next sync tries again.'])
  assert.deepEqual(pinnedNotes(3), ['3 messages could not be opened yet. The next sync tries again.'])
})
