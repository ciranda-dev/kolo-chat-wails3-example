// A stand-in for the Go service and the Wails runtime, with sample data, for design work in a browser.
// dev/vite.config.js loads it in place of both; the app build never does. Nothing here touches the network.

const ME = '5c9e2b7a41d08f63e2a7c4b19d50f8e3a6b2c71d94e0f5a8b3c6d2e19f47a0b5'
const LEO = 'b81f4d2c6a09e7f35d1c8b4a2e6f90d7c3a5b18e4f2d6c09a7b3e5f1d8c4a260'
const PRIYA = 'e4a7c19b3d5f0826a1c7e93b5d2f48a0c6e1b7d39f5a2c84e0b6d1f37a9c5e28'
const SAM = '2d6b9f1e5a3c70b84f2e6d1a9c5b37f0e8d4a2c6b19f73e5d0a8c4b2f6e19d37'

const LEO_WORK = '7a3c5e19b2d4f60817a3c5e19b2d4f60817a3c5e19b2d4f60817a3c5e19b2d4f6'
const ME_WORK = '93e1c7a5b3d9f17e5c3a1b9d7f5e3c1a9b7d5f3e1c9a7b5d3f1e9c7a5b3d1f9e'
const ME_BOOKS = '4f8a2c6e0b1d3f5a7c9e2b4d6f8a1c3e5b7d9f2a4c6e8b1d3f5a7c9e2b4d6f8a'

// The conversations of the scene, from ?scene=: chat (default, three), many (a long list of rooms), own-network, one, none (no conversation, pins set), empty,
// busy, failed (one did not start), offline (the three rooms are offline, reconnecting and connecting), no-pin (one conversation, no pin), join, join-error, join-wait (no conversation, no pins),
// start-error, other-layout,
// nulls (every list of every event and method as null, as Go sends a nil slice) or crash (a member whose name is not text).
const scene = new URLSearchParams(globalThis.location?.search ?? '').get('scene') || 'chat'
const ROWS = {
  lake: { id: 'a1b2c3d4e5f60718293a4b5c6d7e8f90', name: 'Lake trip', me: ME, joined: true, admin: false, error: '', folder: '' },
  work: { id: 'b2c3d4e5f60718293a4b5c6d7e8f90a1', name: 'Work', me: ME_WORK, joined: true, admin: true, error: '', folder: '' },
  books: { id: 'c3d4e5f60718293a4b5c6d7e8f90a1b2', name: 'Book club', me: ME_BOOKS, joined: true, admin: false, error: '', folder: '' },
  broken: { id: 'd4e5f60718293a4b5c6d7e8f90a1b2c3', name: 'Notes', me: '', joined: true, admin: false, error: 'The data folder /Users/maya/Library/Application Support/kolo-chat/conversations/d4e5f60718293a4b5c6d7e8f90a1b2c3 holds logs that do not use the derived entry keys, and Kolo Chat cannot use them.', folder: '/Users/maya/Library/Application Support/kolo-chat/conversations/d4e5f60718293a4b5c6d7e8f90a1b2c3' },
}
const SCENES = { chat: ['lake', 'work', 'books'], many: ['lake', 'work', 'books', 'm0', 'm1', 'm2', 'm3', 'm4', 'm5', 'm6', 'm7', 'm8', 'm9'], 'own-network': ['lake'], busy: ['lake', 'work', 'books'], failed: ['lake', 'broken', 'work'], offline: ['lake', 'work', 'books'], one: ['lake'], 'no-pin': ['lake'], empty: ['lake'], nulls: ['lake'], crash: ['lake'] }
// The rooms of the long list: a name each, no messages.
;['Garden club', 'Apartment 4B', 'Reading group', 'Hiking', 'Board games', 'Movie night', 'Cooking', 'Family', 'Neighbors', 'Chess'].forEach((name, i) => {
  ROWS[`m${i}`] = { id: `${i}`.padStart(2, '0').repeat(16), name, me: ME, joined: true, admin: false, error: '', folder: '' }
})
const STATES = { lake: 'offline', work: 'reconnecting', books: 'connecting' }
let rows = (SCENES[scene] ?? []).map((k) => {
  const row = scene === 'nulls' ? { ...ROWS[k], admin: true } : ROWS[k]
  return row.error ? row : { ...row, state: scene === 'offline' ? STATES[k] : 'online' }
})
const hasPins = !scene.startsWith('join') && scene !== 'no-pin'
const LAKE = ROWS.lake.id

const membersOf = {
  [LAKE]: [
    { key: LEO, name: 'Leo', avatar: '🦊', admin: true, seat: true },
    { key: PRIYA, name: 'Priya', avatar: '', admin: true },
    { key: ME, name: 'Maya', avatar: '🌿' },
    { key: SAM, name: 'Sam', avatar: '', pending: true },
  ],
  [ROWS.work.id]: [{ key: ME_WORK, name: 'Maya', avatar: '🌿', admin: true, seat: true }, { key: LEO_WORK, name: 'Leo', avatar: '' }, { key: SAM, name: 'Sam', avatar: '', flagged: true }],
  [ROWS.books.id]: [{ key: PRIYA, name: 'Priya', avatar: '', admin: true, seat: true }, { key: ME_BOOKS, name: 'Maya', avatar: '' }],
}
const roomsOf = { [LAKE]: ['general', 'photos', 'random'], [ROWS.work.id]: ['general', 'releases'], [ROWS.books.id]: ['general'] }
let notch = { supported: true, enabled: true, showText: true }
const PINS = [
  { addr: '203.0.113.7:4701', key: '9a3f61c2d07b48e5a1c69f20b7d3e845c0a2f6b19d73e8a45b1c06f2d9e7a384', push: '', pull: '' },
  { addr: '198.51.100.24:4701', key: '41d8e07a2b6c93f5d0a7e1b48c2f69a3e5d1b07c84f2a6e93b0d5c17f8a2e640', push: 'The pin at 198.51.100.24:4701 did not answer.', pull: '' },
]

// The conversation of the first room: each entry is [author, text, via, day offset, hour, minute]. The first three came
// from the pin while this window was closed. Two messages in a row from one author within five minutes group; a gap of
// more than five minutes, or another author, starts a new group; the day changes once.
const INVITE = 'koloinv0cvpuq5uxcf4cuuhaek7ozqi2w3ithyl4bp3ppismypuua5r3zxv3vjq7jgybuwc4pmqx3qs4myp242hizoja'
const CONVERSATION = [
  [LEO, 'Are we still on for the lake trip on Saturday?', 'pin', -1, 20, 41],
  [LEO, 'The forecast looks good, no rain until Sunday.', 'pin', -1, 20, 42],
  [PRIYA, 'Yes. I can drive, and the car has room for four.', 'pin', -1, 20, 55],
  [ME, "Great, I'll bring snacks and the trail map.", '', 0, 9, 10],
  [ME, 'Should I also bring the small stove?', '', 0, 9, 11],
  [SAM, 'Count me in. What time do we leave?', 'direct', 0, 9, 12],
  [LEO, "Let's meet at the station at 8:30, before the traffic.", 'direct', 0, 9, 14],
  [LEO, 'I will wait at the east entrance, near the bakery.', 'direct', 0, 9, 14],
  [PRIYA, "Works for me. I'll check the weather again tonight.", 'direct', 0, 9, 31],
  [ME, 'Perfect. See you all at 8:30.', '', 0, 9, 33],
  [ME, `Here is the invite for Ana: ${INVITE}`, '', 0, 9, 41],
]
const stamp = (days, h, m) => {
  const d = new Date()
  d.setDate(d.getDate() + days)
  d.setHours(h, m, 0, 0)
  return d.getTime()
}

const handlers = new Map()
const emit = (name, data) => handlers.get(name)?.forEach((cb) => cb({ name, data }))
/** An event of one conversation, as Go sends it: { conv, data }. */
const emitIn = (conv, name, data) => emit(name, { conv, data })
const list = () => rows.map((r) => ({ ...r }))
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

// The reports the window sent to ReportError, for the tests.
globalThis.reports = []

export const Clipboard = {
  async SetText(text) {
    globalThis.copied = text
  },
  async Text() {
    return ''
  },
}

export const Events = {
  On(name, cb) {
    if (!handlers.has(name)) {
      handlers.set(name, new Set())
    }
    handlers.get(name).add(cb)
    return () => handlers.get(name)?.delete(cb)
  },
}

let started = false

/** Plays the sample conversation once, after React has mounted the window (twice, in strict mode). */
async function play() {
  for (let i = 0; i < 50 && !document.querySelector('.composer'); i++) {
    await pause(20)
  }
  if (rows.length === 0) {
    return
  }
  if (scene === 'nulls') {
    emitIn(LAKE, 'chat:peers', null)
    emitIn(LAKE, 'chat:pin', { error: '', warning: '', locked: 0, pins: null })
    emitIn(LAKE, 'chat:profiles', null)
    emitIn(LAKE, 'chat:rooms', null)
    emitIn(LAKE, 'chat:history', null)
    emitIn(LAKE, 'chat:invite-request', null)
    emitIn(LAKE, 'chat:relays', { seq: 1, from: '', relays: null })
    emitIn('', 'chat:relays', { seq: 1, from: '', relays: null })
    emitIn(LAKE, 'chat:join', null)
    emitIn(LAKE, 'chat:state', { pendingRooms: null })
    emit('notch:state', null)
    return
  }
  if (scene === 'crash') {
    emitIn(LAKE, 'chat:profiles', [{ key: ME, name: { text: 'not text' }, avatar: '', admin: true }])
    return
  }
  emitIn(LAKE, 'chat:peers', scene === 'offline' ? 0 : 3)
  emitIn(LAKE, 'chat:pin', { error: '', warning: scene === 'busy' ? 'One pin does not answer. The other keeps your messages.' : '', locked: scene === 'busy' ? 2 : 0, pins: hasPins ? PINS : [] })
  if (scene === 'busy') {
    emitIn(LAKE, 'chat:invite-request', { request: 1, invite: 'inv1', joiner: 'c7e2a9b4d15f0836', withdrawn: false })
    emitIn(LAKE, 'chat:invite-request', { request: 2, invite: 'inv1', joiner: '0f4b8d21a6c93e57', withdrawn: false })
    emitIn(ROWS.work.id, 'chat:invite-request', { request: 3, invite: 'inv2', joiner: '5d1e7c3a9b2f4086', withdrawn: false })
    emitIn(LAKE, 'chat:state', { paused: 1, pendingRooms: ['photos'], pending: 2 })
  }
  if (scene === 'empty') {
    return
  }
  for (const [from, text, via, days, h, m] of CONVERSATION) {
    emitIn(LAKE, 'chat:msg', { from, room: 'general', text, via, time: stamp(days, h, m), mine: from === ME })
  }
  if (rows.some((r) => r.id === ROWS.work.id)) {
    emitIn(ROWS.work.id, 'chat:msg', { from: LEO_WORK, room: 'releases', text: 'The build is green.', via: 'direct', time: stamp(0, 8, 5) })
    emitIn(ROWS.work.id, 'chat:msg', { from: LEO_WORK, room: 'general', text: 'Stand-up moved to ten.', via: 'pin', time: stamp(0, 8, 20) })
    emitIn(ROWS.work.id, 'chat:msg', { from: LEO_WORK, room: 'launch', text: 'Launch notes are in.', via: 'pin', time: stamp(0, 8, 21) })
  }
  if (rows.some((r) => r.id === ROWS.books.id)) {
    emitIn(ROWS.books.id, 'chat:msg', { from: PRIYA, room: 'general', text: 'Next month we read the one about the lighthouse.', via: 'pin', time: stamp(-3, 19, 2) })
  }
}

export async function Start() {
  if (!started) {
    started = true
    setTimeout(play, 50)
  }
  if (scene === 'start-error') {
    throw new Error('kolo-chat: could not open the data folder: permission denied')
  }
  if (scene === 'other-layout') {
    throw Object.assign(new Error(`The data folder ${DATA} is not in a layout Kolo Chat can open (layout 4).`), { cause: { dir: DATA, layout: 4 } })
  }
  const network = scene === 'own-network' ? { mode: 'own', nodes: ['127.0.0.1:7447', '127.0.0.1:7448'] } : { mode: 'public', nodes: [] }
  return { conversations: list(), maxConversations: 64, hasPins, network, first: rows[0]?.id ?? '', version: 'dev' }
}

export async function List() {
  return list()
}

const DATA = '/Users/maya/Library/Application Support/kolo-chat'
let leaveToken = ''
let made = 0

/** A new row, as Create and Join make one: a folder of its own and a kolo-id of its own. */
function addRow(name) {
  made++
  const row = { id: `e${made}`.padEnd(32, '0'), name: name.trim() || 'Conversation', me: `${made}`.repeat(64).slice(0, 64), joined: true, admin: true, state: 'online', error: '', folder: '' }
  rows = [...rows, row]
  emit('chat:conversations', list())
  return { ...row }
}

export async function Create(name) {
  return addRow(name)
}

export async function Focus(conv) {}

export async function Rename(conv, name) {
  rows = rows.map((r) => (r.id === conv ? { ...r, name: name.trim() } : r))
  emit('chat:conversations', list())
}

export async function PlanLeave(conv) {
  leaveToken = `token${Math.random()}`
  const row = rows.find((r) => r.id === conv)
  const others = rows.filter((r) => r.id !== conv)
  return {
    token: leaveToken,
    name: row?.name ?? '',
    dir: `${DATA}/conversations/${conv}`,
    delete: [`${DATA}/conversations/${conv}`],
    keep: [`${DATA}/layout.json`, `${DATA}/notch.json`, ...others.map((r) => r.name)],
    others: others.length,
    founder: conv === ROWS.work.id,
    admins: [],
    configured: false,
  }
}

export async function CancelLeave(conv) {}

export async function Leave(conv, token, to) {
  if (!token || token !== leaveToken) {
    throw new Error('Confirm first: open Leave conversation again and read what it deletes.')
  }
  leaveToken = ''
  rows = rows.filter((r) => r.id !== conv)
  globalThis.left = true
  emit('chat:conversations', list())
}

export async function Members(conv) {
  return scene === 'nulls' ? null : (membersOf[conv] ?? [])
}

export async function Rooms(conv) {
  return scene === 'nulls' ? null : (roomsOf[conv] ?? ['general'])
}

export async function IsAdmin(conv) {
  return rows.find((r) => r.id === conv)?.admin ?? false
}

export async function RemoveMember(conv, key) {
  globalThis.removed = key
  membersOf[conv] = (membersOf[conv] ?? []).filter((m) => m.key !== key)
  emitIn(conv, 'chat:profiles', membersOf[conv])
}

export async function Send(conv, room, text) {}

export async function SetProfile(conv, name, avatar) {
  const me = rows.find((r) => r.id === conv)?.me
  membersOf[conv] = (membersOf[conv] ?? []).map((m) => (m.key === me ? { ...m, name, avatar } : m))
  emitIn(conv, 'chat:profiles', membersOf[conv])
}

export async function AddRoom(conv, name) {
  roomsOf[conv] = [...(roomsOf[conv] ?? ['general']), name.trim().toLowerCase()]
  emitIn(conv, 'chat:rooms', roomsOf[conv])
}

let invites = []

export async function CreateInvite(conv, opts) {
  const id = `inv${invites.length + 1}`.padEnd(16, '0')
  const info = {
    id,
    code: 'kolo-chat-invite:sample-code-for-the-mock-only',
    expires: new Date(Date.now() + (opts?.ttlSeconds ?? 3600) * 1000).toISOString(),
    uses: opts?.uses ?? 1,
    used: 0,
    state: 'active',
  }
  invites = [...invites, info]
  return info
}

export async function Invites(conv) {
  return scene === 'nulls' ? null : invites.map(({ code, ...rest }) => rest)
}

export async function Revoke(conv, id) {
  invites = invites.map((i) => (i.id === id ? { ...i, state: 'revoked' } : i))
}

export async function Join(code, name) {
  if (scene === 'join-error') {
    throw new Error('This invite has expired. Ask for a new one.')
  }
  if (scene === 'join-wait') {
    emitIn('f'.repeat(32), 'chat:join', { status: 'waiting', message: 'Waiting for an admin to approve your request...' })
    await new Promise(() => {})
  }
  return addRow(name)
}
export async function CancelJoin() {}
export async function Approve(conv, request, admin) {
  globalThis.approved = { request, admin }
}
export async function Deny(conv, request) {}

export async function State() {
  return { ...notch, hasMessage: false, unread: 0, peers: 3, pin: 'ok', locked: 0 }
}

export async function Settings() {
  return notch
}

export async function SetEnabled(on) {
  notch = { ...notch, enabled: on }
  emit('notch:state', await State())
}

export async function SetShowText(on) {
  notch = { ...notch, showText: on }
  emit('notch:state', await State())
}

export async function SetLanguage(lang) {}
export async function ReportError(message, stack) {
  globalThis.reports.push({ message, stack })
}
export async function Hide() {}
export async function Hold() {}
export async function Release() {}
export async function Show() {}
export async function OpenChat() {}
