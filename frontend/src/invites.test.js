import test from 'node:test'
import assert from 'node:assert/strict'
import { shownCode, copyCode } from './invites.js'

test('shownCode gives the code of an active invite only', () => {
  const codes = { a: 'CODE-A', b: 'CODE-B' }
  assert.equal(shownCode({ id: 'a', state: 'active' }, codes), 'CODE-A')
  for (const state of ['expired', 'used up', 'revoked']) {
    assert.equal(shownCode({ id: 'b', state }, codes), '', state)
  }
  assert.equal(shownCode({ id: 'c', state: 'active' }, codes), '')
  assert.equal(shownCode(null, codes), '')
})

test('copyCode uses the Wails clipboard, then the browser one', async () => {
  const got = []
  assert.equal(await copyCode('CODE', async (s) => got.push(['wails', s]), async (s) => got.push(['browser', s])), true)
  assert.deepEqual(got, [['wails', 'CODE']])
  got.length = 0
  const fail = async () => { throw new Error('no clipboard') }
  assert.equal(await copyCode('CODE', fail, async (s) => got.push(['browser', s])), true)
  assert.deepEqual(got, [['browser', 'CODE']])
  assert.equal(await copyCode('CODE', fail, fail), false)
  assert.equal(await copyCode('CODE', fail, undefined), false)
  assert.equal(await copyCode('', async () => got.push('x')), false)
})
