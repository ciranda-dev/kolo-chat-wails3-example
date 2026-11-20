import test from 'node:test'
import assert from 'node:assert/strict'
import { list, pinStatus } from './lists.js'

test('list reads null, a missing field or a non-list as the fallback, as Go sends a nil slice as null', () => {
  assert.deepEqual(list(null), [])
  assert.deepEqual(list(undefined), [])
  assert.deepEqual(list({}), [])
  assert.deepEqual(list(null, ['general']), ['general'])
  const rooms = ['general', 'dev']
  assert.equal(list(rooms, ['general']), rooms)
})

test('pinStatus reads a "chat:pin" payload with null pins, or none, with every field in place', () => {
  const empty = { error: '', warning: '', locked: 0, pins: [] }
  assert.deepEqual(pinStatus(null), empty)
  assert.deepEqual(pinStatus({ error: '', warning: '', locked: 0, pins: null }), empty)
  const pins = [{ addr: '127.0.0.1:4701', key: 'aa', push: '', pull: '' }]
  assert.deepEqual(pinStatus({ error: 'down', locked: 2, pins }), { ...empty, error: 'down', locked: 2, pins })
})
