import test from 'node:test'
import assert from 'node:assert/strict'
import { handoverAdmins, leaveNotes, leaveStep, leaveTarget, leaveToken } from './leave.js'
import { strings } from './i18n.js'

const plan = { token: 't1', dir: '/data', delete: ['/data/conversation.json', '/data/store'], keep: ['/data/identity.key'] }

test('Leave gets its token only after a confirm with a plan', () => {
  const idle = { step: 'idle' }
  assert.equal(leaveToken(idle), '')
  assert.deepEqual(leaveStep(idle, { type: 'confirm' }), idle, 'no confirm without a plan')

  const confirm = leaveStep(idle, { type: 'plan', plan })
  assert.equal(confirm.step, 'confirm')
  assert.equal(confirm.plan, plan, 'the plan shown is the one from Go, as it is')
  assert.equal(leaveToken(confirm), '', 'showing the plan is not a confirm')

  const leaving = leaveStep(confirm, { type: 'confirm' })
  assert.equal(leaving.step, 'leaving')
  assert.equal(leaveToken(leaving), 't1')
  assert.deepEqual(leaveStep(leaving, { type: 'confirm' }), leaving, 'a second click does nothing')

  assert.deepEqual(leaveStep(confirm, { type: 'cancel' }), idle)
  assert.equal(leaveToken(leaveStep(leaveStep(confirm, { type: 'cancel' }), { type: 'confirm' })), '', 'cancel drops the plan')
  assert.deepEqual(leaveStep(leaving, { type: 'left' }), idle)

  const failed = leaveStep(leaving, { type: 'error', error: 'disk' })
  assert.deepEqual(failed, { step: 'failed', error: 'disk' })
  assert.equal(leaveToken(leaveStep(failed, { type: 'confirm' })), '', 'a failure needs a new plan')
  assert.equal(leaveStep(idle, { type: 'plan', plan: { token: '' } }).step, 'failed', 'a plan without a token')
})

test('leaveNotes always warns, and says what applies', () => {
  assert.deepEqual(leaveNotes(plan), ['settings.leave.warning', 'settings.leave.then'])
  assert.deepEqual(leaveNotes({ ...plan, founder: true, admins: [], configured: true }), [
    'settings.leave.warning',
    'state.founderLeave',
    'settings.leave.configured',
    'settings.leave.then',
  ])
  assert.deepEqual(leaveNotes({ ...plan, founder: true, admins: [{ key: 'b' }] }), ['settings.leave.warning', 'state.handoverNote', 'settings.leave.then'])
  assert.deepEqual(leaveNotes({ ...plan, admins: [{ key: 'b' }] }), ['settings.leave.warning', 'settings.leave.then'], 'only the founder hands over')
})

test('S10: a founder\'s confirm names the admin it hands over to, and only the founder gets the choice', () => {
  const confirm = leaveStep({ step: 'idle' }, { type: 'plan', plan: { ...plan, founder: true, admins: [{ key: 'b', name: 'Bob' }] } })
  assert.deepEqual(handoverAdmins(confirm.plan).map((a) => a.key), ['b'])
  assert.deepEqual(handoverAdmins({ ...plan, admins: [{ key: 'b' }] }), [])
  assert.deepEqual(handoverAdmins(null), [])
  const leaving = leaveStep(confirm, { type: 'confirm', to: 'b' })
  assert.equal(leaveTarget(leaving), 'b')
  assert.equal(leaveTarget(leaveStep(confirm, { type: 'confirm' })), '')
  assert.equal(leaveTarget(confirm), '')
})

test('T23: every note of the plan is a sentence in each language, and Leave speaks of one room of several', () => {
  for (const key of [...leaveNotes({ ...plan, founder: true, configured: true, others: 2 }), ...leaveNotes({ ...plan, founder: true, admins: [{}] })]) {
    for (const lang of ['en', 'es', 'pt']) {
      assert.ok(strings[lang][key], `${lang} ${key}`)
    }
  }
  assert.match(strings.en['settings.leave.warning'], /its folder, with its key, its messages, its invites and your kolo-id in it\. Your other rooms stay\./)
  assert.match(strings.en['settings.leave.then'], /new kolo-id/)
  assert.ok(!strings.en['settings.leave.then'].includes('stays the same'))
})
