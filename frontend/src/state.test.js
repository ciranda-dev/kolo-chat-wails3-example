import test from 'node:test'
import assert from 'node:assert/strict'
import { canRemove, isAdmin, memberMarks, stateBanners, stateStatus } from './state.js'
import { strings } from './i18n.js'

const founder = { key: 'a', admin: true, seat: true }
const admin = { key: 'b', admin: true }
const member = { key: 'c', pending: true, flagged: true }
const members = [founder, admin, member]

test('S16: Remove is for admins only, never on the settler, never on yourself', () => {
  assert.ok(isAdmin(members, 'b') && !isAdmin(members, 'c') && !isAdmin(null, 'a'))
  assert.ok(canRemove(members, 'b', member))
  assert.ok(!canRemove(members, 'b', founder), 'the settler cannot be removed')
  assert.ok(!canRemove(members, 'c', admin), 'a member removes nobody')
  assert.ok(!canRemove(members, 'b', admin), 'not yourself')
  assert.ok(canRemove(members, 'a', admin), 'the founder removes another admin')
})

test('S16: marks for admins, pending and flagged members', () => {
  assert.deepEqual(memberMarks(founder), ['state.admin'])
  assert.deepEqual(memberMarks(member), ['state.pending', 'state.flagged'])
  assert.deepEqual(memberMarks({ key: 'e', mismatch: true }), ['state.mismatch'])
  assert.deepEqual(memberMarks({ key: 'd' }), [])
})

test('S16 and N6: each state has its sentence, and a missing payload has none', () => {
  assert.deepEqual(stateBanners(stateStatus(null)), [])
  const all = stateStatus({ halted: true, settlementHalted: true, diverged: true, unconfirmed: true, noAnswer: true, otherGroup: true, notListed: true, paused: 2 })
  assert.deepEqual(stateBanners(all).map((b) => b.key), ['state.diverged', 'state.halted', 'state.settlementHalted', 'state.otherGroup',
    'state.notListed', 'state.noAnswer', 'state.paused', 'state.unconfirmed'])
  assert.equal(stateBanners(all).find((b) => b.key === 'state.paused').n, 2)
  assert.deepEqual(stateBanners(stateStatus({ strandLost: true, unconfirmed: true })).map((b) => b.key), ['state.strandLost'])
  assert.deepEqual(stateStatus({ pendingRooms: null }).pendingRooms, [])
  for (const b of stateBanners(all)) {
    for (const lang of ['en', 'es', 'pt']) {
      assert.ok(strings[lang][b.key], `${lang} ${b.key}`)
    }
  }
})

test('the removal sentences say it is not revocation, and never claim more', () => {
  for (const lang of ['en', 'es', 'pt']) {
    for (const key of ['state.remove.confirm', 'state.removeNote']) {
      assert.ok(strings[lang][key].includes(lang === 'en' ? 'key' : lang === 'es' ? 'clave' : 'chave'), `${lang} ${key}`)
    }
  }
  const en = strings.en['state.remove.confirm'] + strings.en['state.removeNote']
  assert.match(en, /keep the room key/)
  for (const word of ['revoke', 'ban', 'kick', 'everyone']) {
    assert.ok(!en.toLowerCase().includes(word), word)
  }
})
