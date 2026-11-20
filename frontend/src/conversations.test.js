import test from 'node:test'
import assert from 'node:assert/strict'
import { convOf, initialState, reduce, shownRows } from './conversations.js'

const rows = [
  { id: 'a', name: 'Family', joined: true, error: '' },
  { id: 'b', name: 'Work', joined: true, error: '' },
  { id: 'p', name: 'Conversation', joined: false, error: '' },
  { id: 'c', name: 'Old', joined: false, error: 'conversation.json: damaged' },
]
const event = (name, conv, data) => ({ type: 'event', name, payload: { conv, data } })
const run = (state, ...actions) => actions.reduce(reduce, state)

test('the list shows the conversations and the failed ones, not the join that waits, and selects the first', () => {
  assert.deepEqual(shownRows(rows).map((r) => r.id), ['a', 'b', 'c'])
  assert.deepEqual(shownRows(null), [])
  const s = run(initialState, { type: 'list', list: rows })
  assert.equal(s.selected, 'a')
  assert.equal(run(initialState, { type: 'list', list: null }).selected, '')
})

test('T21: events go to their conversation, unread counts only the others, and the history never', () => {
  let s = run(initialState, { type: 'list', list: rows })
  s = run(s,
    event('chat:msg', 'a', { from: 'x', text: 'seen' }),
    event('chat:msg', 'b', { from: 'x', text: 'one' }),
    event('chat:msg', 'b', { from: 'x', text: 'two' }),
    event('chat:history', 'b', [{ from: 'x', text: 'old', via: 'history' }]),
    event('chat:peers', 'b', 3),
    event('chat:profiles', 'a', null),
    event('chat:rooms', 'b', null),
    event('chat:invite-request', 'a', { request: 7, joiner: 'cc' }),
    event('chat:invite-request', 'a', null),
  )
  assert.deepEqual(convOf(s, 'a').messages.map((m) => m.text), ['seen'])
  assert.deepEqual(convOf(s, 'b').messages.map((m) => m.text), ['old', 'one', 'two'])
  assert.equal(convOf(s, 'a').unread, 0)
  assert.equal(convOf(s, 'b').unread, 2)
  assert.equal(convOf(s, 'b').peers, 3)
  assert.equal(convOf(s, 'a').peers, 0)
  assert.deepEqual(convOf(s, 'a').members, [], 'a null list stays a list')
  assert.deepEqual(convOf(s, 'b').rooms, ['general'])
  assert.deepEqual(convOf(s, 'a').requests.map((r) => r.request), [7])
  assert.equal(new Set([...convOf(s, 'a').messages, ...convOf(s, 'b').messages].map((m) => m.id)).size, 4, 'ids stay unique')

  s = run(s, { type: 'select', id: 'b' })
  assert.equal(convOf(s, 'b').unread, 0, 'selecting reads it')
  s = run(s, event('chat:msg', 'b', { text: 'three' }), event('chat:msg', 'a', { text: 'four' }))
  assert.equal(convOf(s, 'b').unread, 0)
  assert.equal(convOf(s, 'a').unread, 1)
  assert.equal(run(s, event('chat:msg', '', { text: 'nowhere' })), s, 'an event without a conversation changes nothing')
  s = run(s, { type: 'request-done', conv: 'a', request: 7 }, { type: 'sent', conv: 'b', from: 'me', room: 'general', text: 'mine' })
  assert.deepEqual(convOf(s, 'a').requests, [])
  assert.equal(convOf(s, 'b').messages.at(-1).mine, true)
})

test('T21: when the selected conversation leaves, the next one is selected, else the one before, and its state goes', () => {
  let s = run(initialState, { type: 'list', list: rows }, { type: 'select', id: 'a' }, event('chat:msg', 'a', { text: 'x' }))
  s = run(s, { type: 'list', list: rows.filter((r) => r.id !== 'a') })
  assert.equal(s.selected, 'b')
  assert.equal(s.convs.a, undefined)
  const middle = run(initialState, { type: 'list', list: rows }, { type: 'select', id: 'b' }, { type: 'list', list: rows.filter((r) => r.id !== 'b') })
  assert.equal(middle.selected, 'c', 'the one after it, not the first')
  s = run(s, { type: 'select', id: 'c' }, { type: 'list', list: rows.filter((r) => r.id === 'b') })
  assert.equal(s.selected, 'b', 'the one before')
  s = run(s, { type: 'list', list: [] })
  assert.equal(s.selected, '')
  const kept = run(initialState, { type: 'list', list: rows }, { type: 'select', id: 'b' }, { type: 'list', list: [...rows, { id: 'd', joined: true }] })
  assert.equal(kept.selected, 'b', 'a new row keeps the selection')
})

test('T22: relays keep the newest by seq per conversation, and the app has its own', () => {
  const st = (seq, state) => ({ seq, from: '-relay', relays: [{ addr: 'r', key: 'k', state, error: state === 'failed' ? 'wrong-key' : '' }] })
  let s = run(initialState, { type: 'list', list: rows },
    event('chat:relays', 'a', st(5, 'failed')),
    event('chat:relays', 'a', st(4, 'waiting')),
    event('chat:relays', 'b', st(9, 'ready')),
    event('chat:relays', '', st(2, 'failed')),
    event('chat:relays', '', st(1, 'waiting')),
  )
  assert.equal(convOf(s, 'a').relays.seq, 5)
  assert.equal(convOf(s, 'a').relays.relays[0].state, 'failed', 'an older event of A does not hide its wrong key')
  assert.equal(convOf(s, 'b').relays.relays[0].state, 'ready', "B's seq is its own")
  assert.equal(s.relays.relays[0].state, 'failed')
  s = run(s, event('chat:relays', 'b', st(1, 'waiting')))
  assert.equal(convOf(s, 'b').relays.seq, 9)
})

test('CV-6: each conversation keeps its own draft, and sending clears only its own', () => {
  let s = run(initialState, { type: 'list', list: rows }, { type: 'draft', conv: 'b', text: 'reply to work' }, { type: 'select', id: 'a' })
  assert.equal(convOf(s, 'a').draft, '', 'another conversation starts empty')
  assert.equal(convOf(s, 'b').draft, 'reply to work')
  s = run(s, { type: 'draft', conv: 'a', text: 'for the family' }, { type: 'sent', conv: 'a', from: 'me', room: 'general', text: 'for the family' })
  assert.equal(convOf(s, 'a').draft, '')
  assert.equal(convOf(s, 'b').draft, 'reply to work')
})

test('S16: chat:state reaches its conversation only, with every field in place', () => {
  let state = reduce(initialState, { type: 'list', list: [{ id: 'a', joined: true }, { id: 'b', joined: true }] })
  state = reduce(state, { type: 'event', name: 'chat:state', payload: { conv: 'a', data: { paused: 2, pendingRooms: null } } })
  assert.equal(convOf(state, 'a').state.paused, 2)
  assert.deepEqual(convOf(state, 'a').state.pendingRooms, [])
  assert.equal(convOf(state, 'b').state.paused, 0)
})

test('a live message keeps the sender\'s time, else the time it arrived; the history without one has none', () => {
  let s = run(initialState, { type: 'list', list: rows })
  s = run(s,
    { type: 'event', name: 'chat:msg', payload: { conv: 'a', data: { from: 'x', text: 'sent at 5', time: 5 } }, now: 100 },
    { type: 'event', name: 'chat:msg', payload: { conv: 'a', data: { from: 'x', text: 'no time' } }, now: 100 },
    { type: 'event', name: 'chat:history', payload: { conv: 'a', data: [{ from: 'x', text: 'old', via: 'history' }] }, now: 100 },
    { type: 'sent', conv: 'a', from: 'me', room: 'general', text: 'mine', now: 200 })
  assert.deepEqual(convOf(s, 'a').messages.map((m) => [m.text, m.time]), [['old', 0], ['sent at 5', 5], ['no time', 100], ['mine', 200]])
})

test('only the default room, the one the window shows, counts as unread', () => {
  let s = run(initialState, { type: 'list', list: rows })
  s = run(s, event('chat:msg', 'b', { from: 'x', room: 'releases', text: 'elsewhere' }), event('chat:msg', 'b', { from: 'x', room: 'general', text: 'here' }))
  assert.equal(convOf(s, 'b').unread, 1)
  assert.equal(convOf(s, 'b').messages.length, 2, 'the message is kept')
})

test('a room that waits for its turn is listed with its state, and a new list changes the state without losing the room', () => {
  const waiting = [{ id: 'a', name: 'Family', joined: true, error: '', state: 'connecting' }, { id: 'b', name: 'Work', joined: true, error: '', state: 'connecting' }]
  let s = run(initialState, { type: 'list', list: waiting }, event('chat:msg', 'a', { from: 'x', text: 'kept', room: 'general' }), { type: 'select', id: 'b' })
  assert.deepEqual(shownRows(s.list).map((r) => r.state), ['connecting', 'connecting'])
  s = run(s, { type: 'list', list: [{ ...waiting[0], state: 'online' }, { ...waiting[1], state: 'offline' }] })
  assert.deepEqual(s.list.map((r) => r.state), ['online', 'offline'])
  assert.equal(s.selected, 'b')
  assert.equal(convOf(s, 'a').messages.length, 1, 'the state of a room never clears its messages')
})
