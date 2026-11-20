import test from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { NOTCH_EVENT, emptyState, notchModel } from './model.js'

const message = { ...emptyState, hasMessage: true, name: 'Ana', avatar: 'A', room: 'dev', preview: 'Lunch at one?', unread: 2, peers: 3, pin: 'ok' }

test('with the preview off the notch shows a label, not text', () => {
  const m = notchModel({ ...message, showText: false, preview: '' }, 'en')
  assert.equal(m.line, 'New message')
  assert.equal(m.lineIsText, false)
  assert.equal(m.name, 'Ana')
  assert.equal(m.unread, '2 new')
})

test('the notch draws only the fields of notch:state', () => {
  const hostile = { ...message, preview: '', text: 'my PIN is 1234', code: 'INVITE-CODE', members: [{ key: 'aa11' }] }
  const drawn = JSON.stringify(notchModel(hostile, 'en'))
  for (const secret of ['1234', 'INVITE-CODE', 'aa11']) {
    assert.ok(!drawn.includes(secret), secret)
  }
})

test('the notch page listens to notch:state and to no event of the chat', () => {
  assert.equal(NOTCH_EVENT, 'notch:state')
  const dir = new URL('.', import.meta.url)
  const files = readdirSync(dir).filter((f) => /\.jsx?$/.test(f) && !f.endsWith('.test.js'))
  assert.ok(files.includes('live.jsx'))
  for (const f of files) {
    const src = readFileSync(new URL(f, dir), 'utf8')
    assert.ok(!/['"`]chat:/.test(src), `${f} names a chat event`)
    assert.ok(!src.includes('chatservice'), `${f} imports the chat service`)
    for (const [, name] of src.matchAll(/Events\.On\(\s*([^,)]+)/g)) {
      assert.equal(name.trim(), 'NOTCH_EVENT', `${f} listens to ${name}`)
    }
  }
  assert.match(readFileSync(new URL('live.jsx', dir), 'utf8'), /Events\.On\(NOTCH_EVENT/)
})

test('states of the notch', () => {
  assert.deepEqual(
    notchModel(message, 'pt'),
    { label: 'Kolo Chat, mensagem mais recente', room: '# dev', avatar: 'A', name: 'Ana', line: 'Lunch at one?', lineIsText: true,
      unread: '2 novas', live: true, peers: '3 online', pin: 'Pin pronto', pinState: 'ok', open: 'Abrir chat', hide: 'Ocultar' },
  )
  const idle = notchModel(emptyState, 'es')
  assert.equal(idle.line, 'No hay mensajes nuevos')
  assert.equal(idle.unread, '')
  assert.equal(idle.pin, '')
  assert.equal(idle.live, false)
  assert.equal(idle.peers, 'Ningún par en línea')
  // A preview left from before is not drawn once there is no message.
  assert.equal(notchModel({ ...emptyState, preview: 'old', name: 'x', room: 'y' }, 'en').line, 'No new messages')
  assert.equal(notchModel({ ...message, pin: 'waiting', locked: 4 }, 'en').pin, '4 not opened yet')
  assert.equal(notchModel({ ...message, pin: 'error' }, 'en').pinState, 'error')
  assert.equal(notchModel({ ...message, pin: '__proto__' }, 'en').pin, '')
})

test('T24: the room line names the conversation when there is one', () => {
  assert.equal(notchModel({ ...message, conversation: 'Family' }, 'en').room, 'Family · # dev')
  assert.equal(notchModel({ ...message, conversation: '' }, 'en').room, '# dev')
  assert.equal(notchModel({ ...message, conversation: { not: 'text' } }, 'en').room, '# dev')
  assert.equal(notchModel({ ...emptyState, conversation: 'Family', room: 'dev' }, 'en').room, '')
})
