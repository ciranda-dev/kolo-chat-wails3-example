import { list, pinStatus } from './lists.js'
import { toMsg, withHistory } from './messages.js'
import { newerRelays, relayStatus } from './relays.js'
import { DEFAULT_ROOM, stateStatus } from './state.js'

/** The state of one conversation in the window, before any of its events. */
export const emptyConv = () => ({
  messages: [],
  peers: 0,
  pin: pinStatus(null),
  relays: relayStatus(null),
  members: [],
  rooms: ['general'],
  requests: [],
  state: stateStatus(null),
  unread: 0,
  draft: '',
})

/** The window's state: the list from Go, the selected conversation, the state of each one, and the app's relays. */
export const initialState = { list: [], selected: '', convs: {}, relays: relayStatus(null), nextId: 1 }

/** The rows the window lists: the conversations, and those that did not start, but not the join that waits. */
export const shownRows = (rows) => list(rows).filter((c) => c?.joined || c?.error)

/** The state of the conversation id, empty until it has events. */
export const convOf = (state, id) => state.convs[id] ?? emptyConv()

/**
 * The window's reducer. Each event of a conversation comes as { conv, data }, and goes to that conversation only. A
 * message of the default room counts as unread unless its conversation is the selected one; the history never does. Each conversation
 * keeps its own draft, so a reply typed in one is never sent to another.
 */
export function reduce(state, action) {
  switch (action.type) {
    case 'event':
      return onEvent(state, action.name, action.payload, action.now ?? 0)
    case 'list':
      return onList(state, list(action.list))
    case 'select':
      return { ...state, selected: action.id, convs: { ...state.convs, [action.id]: { ...convOf(state, action.id), unread: 0 } } }
    case 'draft': {
      const c = convOf(state, action.conv)
      return { ...state, convs: { ...state.convs, [action.conv]: { ...c, draft: String(action.text ?? '') } } }
    }
    case 'sent': {
      const c = convOf(state, action.conv)
      const msg = { id: state.nextId, from: action.from, room: action.room, text: action.text, mine: true, via: '', time: action.now ?? 0 }
      return { ...state, nextId: state.nextId + 1, convs: { ...state.convs, [action.conv]: { ...c, messages: [...c.messages, msg], draft: '' } } }
    }
    case 'directory': {
      const c = convOf(state, action.conv)
      return { ...state, convs: { ...state.convs, [action.conv]: { ...c, members: list(action.members), rooms: list(action.rooms, ['general']) } } }
    }
    case 'request-done': {
      const c = convOf(state, action.conv)
      return { ...state, convs: { ...state.convs, [action.conv]: { ...c, requests: c.requests.filter((r) => r.request !== action.request) } } }
    }
  }
  return state
}

function onEvent(state, name, payload, now) {
  const conv = typeof payload?.conv === 'string' ? payload.conv : ''
  const data = payload?.data
  if (conv === '') {
    return name === 'chat:relays' ? { ...state, relays: newerRelays(state.relays, relayStatus(data)) } : state
  }
  const c = convOf(state, conv)
  let nextId = state.nextId
  let next
  switch (name) {
    case 'chat:msg': {
      const msg = toMsg(data, nextId++, now)
      const unseen = conv !== state.selected && msg.room === DEFAULT_ROOM // only the default room shows, so only it is unread
      next = { ...c, messages: [...c.messages, msg], unread: unseen ? c.unread + 1 : c.unread }
      break
    }
    case 'chat:history':
      next = { ...c, messages: withHistory(c.messages, list(data).map((m) => toMsg(m, nextId++))) }
      break
    case 'chat:peers':
      next = { ...c, peers: Number(data) || 0 }
      break
    case 'chat:pin':
      next = { ...c, pin: pinStatus(data) }
      break
    case 'chat:relays':
      next = { ...c, relays: newerRelays(c.relays, relayStatus(data)) }
      break
    case 'chat:profiles':
      next = { ...c, members: list(data) }
      break
    case 'chat:rooms':
      next = { ...c, rooms: list(data, ['general']) }
      break
    case 'chat:state':
      next = { ...c, state: stateStatus(data) }
      break
    case 'chat:invite-request': {
      if (!data) {
        return state
      }
      const rest = c.requests.filter((r) => r.request !== data.request)
      next = { ...c, requests: data.withdrawn ? rest : [...rest, data] }
      break
    }
    default:
      return state
  }
  return { ...state, nextId, convs: { ...state.convs, [conv]: next } }
}

/** A new list: the conversations that left lose their state, and when the selected one left, the next one is selected. */
function onList(state, rows) {
  const convs = { ...state.convs }
  for (const old of state.list) {
    if (!rows.some((r) => r?.id === old.id)) {
      delete convs[old.id]
    }
  }
  const shown = shownRows(rows)
  let selected = state.selected
  if (!shown.some((r) => r.id === selected)) {
    selected = nextSelected(shownRows(state.list), shown, selected)
  }
  return { ...state, list: rows, convs, selected }
}

/** The row to select once selected is gone: the one after it, else the one before, else the first. */
function nextSelected(before, shown, selected) {
  const at = before.findIndex((r) => r.id === selected)
  const has = (r) => shown.some((s) => s.id === r.id)
  if (at >= 0) {
    const after = before.slice(at + 1).find(has) ?? before.slice(0, at).reverse().find(has)
    if (after) {
      return after.id
    }
  }
  return shown[0]?.id ?? ''
}
