import test from 'node:test'
import assert from 'node:assert/strict'
import { headerState, netNotes, networkInfo, networkKey, roomState, stateKey } from './network.js'
import { strings } from './i18n.js'

test('networkInfo is the public network unless the mode says own', () => {
  assert.deepEqual(networkInfo(undefined), { mode: 'public', nodes: [] })
  assert.deepEqual(networkInfo({ mode: 'own', nodes: ['127.0.0.1:4000'] }), { mode: 'own', nodes: ['127.0.0.1:4000'] })
  assert.deepEqual(networkInfo({ mode: 'other', nodes: null }), { mode: 'public', nodes: [] })
})

test('networkKey names the network in use', () => {
  assert.equal(networkKey(networkInfo({ mode: 'public' })), 'settings.network.public')
  assert.equal(networkKey(networkInfo({ mode: 'own' })), 'settings.network.own')
})

test('roomState reads the state of a row and ignores anything else', () => {
  for (const state of ['connecting', 'online', 'reconnecting', 'offline']) {
    assert.equal(roomState({ state }), state)
  }
  assert.equal(roomState({ state: 'failed' }), '')
  assert.equal(roomState({}), '')
  assert.equal(roomState(undefined), '')
})

test('a room that is not online says so in plain words, never as an error that looks final', () => {
  assert.equal(stateKey('online'), '')
  assert.equal(stateKey('connecting'), 'net.connecting')
  assert.equal(stateKey('reconnecting'), 'net.reconnecting')
  assert.equal(stateKey('offline'), 'net.offline')
  assert.equal(stateKey(''), '')
  for (const lang of ['en', 'es', 'pt']) {
    assert.match(strings[lang]['net.offline'], lang === 'en' ? /trying again/ : lang === 'es' ? /vuelve a intentar/ : /tentando de novo/, lang)
    assert.match(strings[lang]['net.connecting'], /\.\.\.$/, lang)
    assert.match(strings[lang]['net.reconnecting'], /\.\.\.$/, lang)
    for (const key of ['net.connecting', 'net.reconnecting', 'net.offline', 'net.kept']) {
      assert.ok(!/error|erro|fall/i.test(strings[lang][key]), `${lang} ${key} is not an error`)
    }
  }
})

test('netNotes says why the network is out, and that what you type is kept', () => {
  assert.deepEqual(netNotes('online', { mode: 'public' }), [])
  assert.deepEqual(netNotes('connecting', { mode: 'public' }), [])
  assert.deepEqual(netNotes('reconnecting', { mode: 'public' }), ['net.kept'])
  assert.deepEqual(netNotes('offline', { mode: 'public' }), ['network.unreachable.public', 'network.options', 'net.kept'])
  assert.deepEqual(netNotes('offline', { mode: 'own' }), ['network.unreachable.own', 'network.options', 'net.kept'])
  assert.deepEqual(netNotes('offline', undefined), ['network.unreachable.public', 'network.options', 'net.kept'])
})

test('the header shows the state only while nobody is online', () => {
  assert.equal(headerState('offline', 0), 'net.offline')
  assert.equal(headerState('reconnecting', 0), 'net.reconnecting')
  assert.equal(headerState('connecting', 0), 'net.connecting')
  assert.equal(headerState('offline', 2), '')
  assert.equal(headerState('online', 0), '')
})

test('every language names the three ways to connect', () => {
  for (const lang of ['en', 'es', 'pt']) {
    assert.match(strings[lang]['network.options'], /-bootstrap host:/, lang)
    assert.match(strings[lang]['network.options'], /-first/, lang)
    assert.match(strings[lang]['settings.network.note'], /-bootstrap/, lang)
    assert.match(strings[lang]['settings.network.public'], /Ciranda/, lang)
  }
})

test('the public network is a sentence with no address and no entry node list; your own is listed', () => {
  for (const lang of ['en', 'es', 'pt']) {
    assert.ok(!/\d+\.\d+\.\d+\.\d+/.test(strings[lang]['settings.network.public']), lang)
    assert.ok(!/:$/.test(strings[lang]['settings.network.public']), `${lang}: it does not introduce a list`)
    assert.match(strings[lang]['settings.network.own'], /-bootstrap/, lang)
  }
  assert.deepEqual(networkInfo({ mode: 'public' }).nodes, [])
})
