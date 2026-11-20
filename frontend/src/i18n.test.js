import test from 'node:test'
import assert from 'node:assert/strict'
import { strings, pickLanguage, t, countKey } from './i18n.js'

test('every language has every string', () => {
  const keys = Object.keys(strings.en).sort()
  for (const lang of ['es', 'pt']) {
    assert.deepEqual(Object.keys(strings[lang]).sort(), keys, lang)
    for (const key of keys) {
      assert.ok(strings[lang][key].trim(), `${lang} ${key}`)
      assert.equal(strings[lang][key].includes('{n}'), strings.en[key].includes('{n}'), `${lang} ${key}`)
    }
  }
})

test('no string has an em dash', () => {
  for (const lang of Object.keys(strings)) {
    for (const [key, text] of Object.entries(strings[lang])) {
      assert.ok(!text.includes(String.fromCharCode(0x2014)), `${lang} ${key}`)
    }
  }
})

test('pickLanguage takes the first language we have', () => {
  assert.equal(pickLanguage(['pt-BR', 'en']), 'pt')
  assert.equal(pickLanguage(['fr-FR', 'es_ES']), 'es')
  assert.equal(pickLanguage(['de']), 'en')
  assert.equal(pickLanguage(), 'en')
  assert.equal(pickLanguage(['constructor']), 'en')
})

test('t fills the count and falls back to English', () => {
  assert.equal(t('en', 'notch.unread.other', 5), '5 new')
  assert.equal(t('pt', 'notch.open'), 'Abrir chat')
  assert.equal(t('fr', 'notch.open'), 'Open chat')
  assert.equal(t('en', 'no.such.key'), 'no.such.key')
})

test('countKey picks the plural form', () => {
  assert.equal(t('en', countKey('en', 'notch.unread', 1), 1), '1 new')
  assert.equal(t('es', countKey('es', 'notch.unread', 3), 3), '3 nuevos')
  assert.equal(t('pt', countKey('pt', 'notch.peers', 0), 0), 'Nenhum par online')
  assert.equal(t('en', countKey('en', 'notch.peers', 1), 1), '1 online')
})

test('the no-pin texts say messages reach people only while they are online, and how to keep them for people who are offline', () => {
  const online = { en: 'online', es: 'en línea', pt: 'online' }
  const offline = { en: 'offline', es: 'desconectado', pt: 'offline' }
  for (const lang of ['en', 'es', 'pt']) {
    for (const key of ['conv.noPins', 'chat.noPin', 'settings.pins.none']) {
      assert.ok(strings[lang][key].includes(online[lang]), `${lang} ${key} says online`)
    }
    assert.ok(strings[lang]['conv.noPins'].includes('-pin') && strings[lang]['conv.noPins'].includes('-pin-file'), `${lang} names the flags`)
    for (const key of ['conv.noPins', 'chat.noPin']) {
      assert.ok(strings[lang][key].includes(offline[lang]), `${lang} ${key} says offline`)
    }
  }
})

test('the window says "room" where a person sees what the app calls a conversation, and keeps the flags', () => {
  const word = { en: /conversation/i, es: /conversaci/i, pt: /conversa/i }
  for (const lang of ['en', 'es', 'pt']) {
    for (const [key, text] of Object.entries(strings[lang])) {
      const without = text.replaceAll('-conversation-key', '').replaceAll('KOLO_CHAT_CONVERSATION_KEY', '').replaceAll('-max-conversations', '').replaceAll('conversation.json', '')
      assert.ok(!word[lang].test(without), `${lang} ${key} still says conversation: ${text}`)
    }
  }
  assert.equal(strings.en['conv.title'], 'Rooms')
  assert.equal(strings.es['conv.title'], 'Salas')
  assert.equal(strings.pt['conv.title'], 'Salas')
  assert.match(strings.en['conv.max'], /-max-conversations/)
  assert.match(strings.es['conv.max'], /-max-conversations/)
  assert.match(strings.pt['conv.max'], /-max-conversations/)
})

test('the two rows of the add menu each have a title and a subtitle, and the line under the composer says how messages travel', () => {
  for (const lang of ['en', 'es', 'pt']) {
    for (const key of ['conv.create.title', 'conv.create.sub', 'conv.join.title', 'conv.join.sub', 'conv.add', 'sidebar.collapse', 'sidebar.expand', 'chat.encrypted', 'conv.noPinsShort']) {
      assert.ok(strings[lang][key].trim(), `${lang} ${key}`)
    }
    assert.notEqual(strings[lang]['conv.create.title'], strings[lang]['conv.join.title'])
  }
  assert.match(strings.en['chat.encrypted'], /encrypted.*straight between the members' computers/)
  assert.match(strings.es['chat.encrypted'], /cifrados.*directo entre las computadoras de los miembros/)
  assert.match(strings.pt['chat.encrypted'], /criptografadas.*direto entre os computadores dos membros/)
})

test('the members and online counts take the plural form', () => {
  assert.equal(t('en', countKey('en', 'chat.members', 1), 1), '1 member')
  assert.equal(t('en', countKey('en', 'chat.members', 4), 4), '4 members')
  assert.equal(t('pt', countKey('pt', 'chat.members', 2), 2), '2 membros')
  assert.equal(t('es', 'chat.online', 3), '3 en línea')
})
