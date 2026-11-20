import { t, countKey } from '../i18n.js'

/** The one event the notch window listens to. The events of the chat go to the main window only. */
export const NOTCH_EVENT = 'notch:state'

/** The state before the app answers. */
export const emptyState = { supported: true, enabled: true, showText: true, hasMessage: false, name: '', avatar: '', room: '', conversation: '', preview: '', unread: 0, peers: 0, pin: 'unknown', locked: 0 }

/** The room line: "<conversation> · # <room>", or "# <room>" without the name of a conversation. */
const roomLine = (conversation, room) => (typeof conversation === 'string' && conversation ? `${conversation} · # ${room}` : `# ${room}`)

/**
 * Everything the notch draws, as text, from the state the app sends. It reads the named fields only, and shows
 * a label in place of the message when the app sent no preview ("Show message text" is off).
 * @param {typeof emptyState} state
 * @param {string} lang
 */
export function notchModel(state, lang) {
  const has = Boolean(state.hasMessage)
  const preview = has && typeof state.preview === 'string' ? state.preview : ''
  const pins = { ok: 'notch.pin.ok', error: 'notch.pin.error', waiting: 'notch.pin.waiting' }
  const unread = Number(state.unread) || 0
  const peers = Number(state.peers) || 0
  return {
    label: t(lang, 'notch.label'),
    room: has && state.room ? roomLine(state.conversation, state.room) : '',
    avatar: has ? String(state.avatar ?? '') : '',
    name: has ? String(state.name ?? '') : '',
    line: preview || t(lang, has ? 'notch.newMessage' : 'notch.idle'),
    lineIsText: preview !== '',
    unread: unread > 0 ? t(lang, countKey(lang, 'notch.unread', unread), unread) : '',
    live: peers > 0,
    peers: t(lang, countKey(lang, 'notch.peers', peers), peers),
    pin: Object.hasOwn(pins, state.pin) ? t(lang, pins[state.pin], Number(state.locked) || 0) : '',
    pinState: Object.hasOwn(pins, state.pin) ? state.pin : '',
    open: t(lang, 'notch.open'),
    hide: t(lang, 'notch.hide'),
  }
}
