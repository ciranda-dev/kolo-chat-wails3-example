import test from 'node:test'
import assert from 'node:assert/strict'
import { folderLayout, folderToMove, startFaultKey } from './fault.js'
import { strings } from './i18n.js'

test('folderToMove reads the folder of the error Go returned', () => {
  const refused = Object.assign(new Error('The data folder /x holds logs Kolo Chat cannot use'), { cause: { dir: '/x', logs: true } })
  assert.equal(folderToMove(refused), '/x')
  assert.equal(folderToMove(Object.assign(new Error('other'), { cause: {} })), '')
  assert.equal(folderToMove(Object.assign(new Error('odd'), { cause: { dir: 7 } })), '')
  assert.equal(folderToMove(null), '')
})

test('T23: a refused data folder of another layout, with unusable logs, or open to others, each has its sentence', () => {
  const refused = (cause) => Object.assign(new Error('refused'), { cause })
  assert.equal(folderLayout(refused({ dir: '/x', layout: 4 })), 4)
  assert.equal(folderLayout(refused({ dir: '/x', layout: '4' })), 0)
  assert.equal(startFaultKey(refused({ dir: '/x', layout: 4 })), 'start.otherLayout')
  assert.equal(startFaultKey(refused({ dir: '/x', layout: -1 })), 'start.otherLayout')
  assert.equal(startFaultKey(refused({ dir: '/x', logs: true })), 'start.moveFolder')
  assert.equal(startFaultKey(new Error('kolo-chat: -pin is malformed')), '')
  assert.equal(startFaultKey(refused({ dir: '/x', open: true })), 'start.openFolder')
  for (const lang of ['en', 'es', 'pt']) {
    assert.match(strings[lang]['start.otherLayout'], /-data/, lang)
    assert.match(strings[lang]['start.openFolder'], /chmod 700/, lang)
  }
})
