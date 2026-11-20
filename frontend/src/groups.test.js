import test from 'node:test'
import assert from 'node:assert/strict'
import { GROUP_GAP, groupMessages } from './groups.js'
import { clock, dayLabel, listStamp } from './times.js'
import { AVATAR_COLORS, avatarColor, initial } from './avatars.js'

const at = (day, h, m, s = 0) => new Date(2026, 10, day, h, m, s).getTime()
let id = 0
const msg = (from, time, extra = {}) => ({ id: ++id, from, mine: false, time, via: '', text: 't', ...extra })
const shape = (items) => items.map((i) => (i.kind === 'day' ? 'day' : `${i.from}:${i.msgs.length}`))

test('two messages from the same author within five minutes are one group, with the author once', () => {
  const items = groupMessages([msg('alice', at(17, 9, 0)), msg('alice', at(17, 9, 1)), msg('alice', at(17, 9, 5))])
  assert.deepEqual(shape(items), ['day', 'alice:3'])
  assert.equal(items[1].msgs.length, 3)
})

test('a new author breaks the group, and the same author again starts another', () => {
  const items = groupMessages([msg('a', at(17, 9, 0)), msg('b', at(17, 9, 1)), msg('a', at(17, 9, 2)), msg('a', at(17, 9, 3))])
  assert.deepEqual(shape(items), ['day', 'a:1', 'b:1', 'a:2'])
})

test('more than five minutes since the message before breaks the group of the same author, exactly five does not', () => {
  assert.equal(GROUP_GAP, 5 * 60 * 1000)
  assert.deepEqual(shape(groupMessages([msg('a', at(17, 9, 0)), msg('a', at(17, 9, 5))])), ['day', 'a:2'])
  assert.deepEqual(shape(groupMessages([msg('a', at(17, 9, 0)), msg('a', at(17, 9, 5, 1))])), ['day', 'a:1', 'a:1'])
  // the gap counts from the message before, not from the start of the group
  assert.deepEqual(shape(groupMessages([msg('a', at(17, 9, 0)), msg('a', at(17, 9, 4)), msg('a', at(17, 9, 8))])), ['day', 'a:3'])
})

test('one of mine and one of theirs with the same key never share a group', () => {
  assert.deepEqual(shape(groupMessages([msg('k', at(17, 9, 0), { mine: true }), msg('k', at(17, 9, 1))])), ['day', 'k:1', 'k:1'])
})

test('a day chip comes before the first message and when the day changes, and it ends the group', () => {
  const items = groupMessages([msg('a', at(16, 23, 58)), msg('a', at(17, 0, 1)), msg('a', at(17, 0, 2))])
  assert.deepEqual(shape(items), ['day', 'a:1', 'day', 'a:2'])
  assert.equal(items[0].time, at(16, 23, 58))
})

test('the group keeps the time of its last message and how its messages arrived, once', () => {
  const [, g] = groupMessages([msg('a', at(17, 9, 0), { via: 'pin' }), msg('a', at(17, 9, 1), { via: 'pin' }), msg('a', at(17, 9, 2), { via: 'peer' })])
  assert.equal(g.time, at(17, 9, 2))
  assert.deepEqual(g.vias, ['pin', 'peer'])
  assert.deepEqual(groupMessages([msg('a', at(17, 9, 0), { via: 'direct' })])[1].vias, [])
})

test('a message with no time never breaks a group by time and never makes a chip', () => {
  assert.deepEqual(shape(groupMessages([msg('a', 0), msg('a', 0), msg('b', 0)])), ['a:2', 'b:1'])
  assert.deepEqual(shape(groupMessages([msg('a', at(17, 9, 0)), msg('a', 0), msg('a', at(17, 9, 1))])), ['day', 'a:3'])
  assert.deepEqual(groupMessages([]), [])
})

test('the day chips and the time of a list read Today, Yesterday, the weekday and the date', () => {
  const now = at(17, 15, 30)
  assert.deepEqual(dayLabel(at(17, 0, 1), now, 'en'), { rel: 'today', text: '' })
  assert.deepEqual(dayLabel(at(16, 23, 59), now, 'en'), { rel: 'yesterday', text: '' })
  assert.match(dayLabel(at(10, 12, 0), now, 'en').text, /Tuesday, November 10/)
  assert.match(dayLabel(new Date(2025, 11, 31, 12).getTime(), now, 'en').text, /2025/)
  assert.deepEqual(listStamp(at(17, 9, 41), now, 'en'), { rel: '', text: clock(at(17, 9, 41), 'en') })
  assert.deepEqual(listStamp(at(16, 9, 41), now, 'en'), { rel: 'yesterday', text: '' })
  assert.match(listStamp(at(14, 9, 41), now, 'en').text, /^Sat/)
  assert.match(listStamp(at(1, 9, 41), now, 'es').text, /nov/)
})

test('an avatar has a color of the palette that depends on its seed, and a letter', () => {
  assert.equal(avatarColor('Lake trip'), avatarColor('Lake trip'))
  assert.ok(AVATAR_COLORS.includes(avatarColor('x')))
  const colors = new Set(['a1b2c3d4', 'b2c3d4e5', 'c3d4e5f6', 'Work', 'Book club', 'Family', 'Chess', 'Hiking'].map(avatarColor))
  assert.ok(colors.size >= 3, 'different seeds do not all get one color')
  assert.equal(initial('  lake'), 'L')
  assert.equal(initial('émile'), 'É')
  assert.equal(initial('🦊 fox'), '🦊')
  assert.equal(initial(''), '?')
  assert.equal(initial(null), '?')
})
