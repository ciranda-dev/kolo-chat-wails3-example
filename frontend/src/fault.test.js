import test from 'node:test'
import assert from 'node:assert/strict'
import { folderLayout, folderToMove, needsRejoin, startFaultKey } from './fault.js'
import { strings } from './i18n.js'

test('needsRejoin reads the rejoin field of the error Go returned', () => {
  const old = Object.assign(new Error('conversation.json: made by an older version'), { cause: { file: 'conversation.json', rejoin: true } })
  assert.equal(needsRejoin(old), true)
  assert.equal(needsRejoin(Object.assign(new Error('conversation.json: damaged'), { cause: {} })), false)
  assert.equal(needsRejoin(new Error('no cause')), false)
  assert.equal(needsRejoin('a string'), false)
  assert.equal(needsRejoin(null), false)
})

test('folderToMove reads the folder of the error Go returned', () => {
  const old = Object.assign(new Error('The data folder /x was made by Kolo Chat 0.1.7 or older'), { cause: { dir: '/x', old: true } })
  assert.equal(folderToMove(old), '/x')
  assert.equal(folderToMove(Object.assign(new Error('other'), { cause: { rejoin: true } })), '')
  assert.equal(folderToMove(Object.assign(new Error('odd'), { cause: { dir: 7 } })), '')
  assert.equal(folderToMove(null), '')
})

test('T23: a refused data folder of 0.1.9 or older, of another layout, or of old logs, each has its sentence', () => {
  const refused = (cause) => Object.assign(new Error('refused'), { cause })
  assert.equal(folderLayout(refused({ dir: '/x', layout: 1 })), 1)
  assert.equal(folderLayout(refused({ dir: '/x', layout: '1' })), 0)
  assert.equal(startFaultKey(refused({ dir: '/x', layout: 1 })), 'start.oldLayout')
  assert.equal(startFaultKey(refused({ dir: '/x', layout: 2 })), 'start.layout2')
  assert.equal(startFaultKey(refused({ dir: '/x', layout: 4 })), 'start.otherLayout')
  assert.equal(startFaultKey(refused({ dir: '/x', layout: -1 })), 'start.otherLayout')
  assert.equal(startFaultKey(refused({ dir: '/x', old: true })), 'start.moveFolder')
  assert.equal(startFaultKey(new Error('kolo-chat: -pin is malformed')), '')
  assert.equal(startFaultKey(refused({ dir: '/x', open: true })), 'start.openFolder')
  for (const lang of ['en', 'es', 'pt']) {
    assert.match(strings[lang]['start.oldLayout'], /0\.1\.9/, lang)
    assert.match(strings[lang]['start.layout2'], /0\.1\.10/, lang)
    assert.match(strings[lang]['start.otherLayout'], /-data/, lang)
    assert.match(strings[lang]['start.openFolder'], /chmod 700/, lang)
  }
})
