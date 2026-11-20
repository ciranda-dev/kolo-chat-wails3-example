import test from 'node:test'
import assert from 'node:assert/strict'
import { toMsg, withHistory } from './messages.js'

test('toMsg reads a chat event, with defaults for what is missing', () => {
  assert.deepEqual(toMsg({ from: 'aa', room: 'dev', text: 'hi', via: 'pin' }, 1),
    { id: 1, from: 'aa', room: 'dev', text: 'hi', mine: false, via: 'pin', time: 0 })
  assert.deepEqual(toMsg({ from: 'bb', text: 'mine', via: 'history', mine: true }, 2),
    { id: 2, from: 'bb', room: 'general', text: 'mine', mine: true, via: 'history', time: 0 })
  assert.deepEqual(toMsg('plain', 3), { id: 3, from: '', room: 'general', text: 'plain', mine: false, via: '', time: 0 })
})

test('toMsg keeps the sender\'s time, and a live message without one gets the time it arrived', () => {
  assert.equal(toMsg({ text: 'a', time: 1795000000000 }, 1, 5).time, 1795000000000)
  assert.equal(toMsg({ text: 'a' }, 1, 5).time, 5)
  assert.equal(toMsg({ text: 'a', time: -3 }, 1).time, 0, 'a history message without a time has none')
})

test('withHistory puts the history above the live messages', () => {
  const live = [toMsg({ text: 'live', via: 'direct' }, 1)]
  const history = [toMsg({ text: 'old', via: 'history' }, 2), toMsg({ text: 'older mine', via: 'history', mine: true }, 3)]
  assert.deepEqual(withHistory(live, history).map((m) => m.text), ['old', 'older mine', 'live'])
})

test('withHistory replaces a history shown before, so nothing shows twice', () => {
  const first = withHistory([], [toMsg({ text: 'old', via: 'history' }, 1)])
  const again = withHistory([...first, toMsg({ text: 'live', via: 'direct' }, 2)], [toMsg({ text: 'old', via: 'history' }, 3)])
  assert.deepEqual(again.map((m) => m.text), ['old', 'live'])
})
