import { useState, useEffect, useReducer, useRef } from 'react'
import { Start, Send, Members, Rooms, Focus } from "../bindings/github.com/ciranda-dev/kolo-chat-wails3-example/conversations.js"
import { Events } from "@wailsio/runtime"
import { ConversationList } from './Conversations.jsx'
import { JoinPanel, InvitePanel } from './Invite.jsx'
import { ChatHeader, Composer, MessageList } from './Chat.jsx'
import { MembersPanel } from './Members.jsx'
import { Page } from './Page.jsx'
import { ProfilePanel } from './Profile.jsx'
import { SettingsPanel } from './Settings.jsx'
import { SetLanguage } from "../bindings/github.com/ciranda-dev/kolo-chat-wails3-example/notchservice.js"
import { browserLanguage, t } from './i18n.js'
import { folderToMove, startFaultKey } from './fault.js'
import { convOf, initialState, reduce, shownRows } from './conversations.js'
import { pinnedNotes } from './pins.js'
import { netNotes, networkInfo, roomState } from './network.js'
import { failedRelays, relayStateKey } from './relays.js'
import { DEFAULT_ROOM, isAdmin, stateBanners } from './state.js'
import { readCollapsed, writeCollapsed } from './sidebar.js'
import './chat.css'

/** The events of the conversations, each { conv, data }; "chat:join" and "chat:invite" go to their panels. */
const CHAT_EVENTS = ['chat:msg', 'chat:history', 'chat:peers', 'chat:pin', 'chat:relays', 'chat:profiles', 'chat:rooms', 'chat:invite-request', 'chat:state']

/** Turns whatever a rejected call threw into a string for the UI. */
function errorText(err) {
  if (err instanceof Error && err.message) {
    return err.message
  }
  if (typeof err === 'string') {
    return err
  }
  return String(err)
}

function App() {
  const [state, dispatch] = useReducer(reduce, initialState)
  const [app, setApp] = useState({ maxConversations: 0, hasPins: true, network: networkInfo(), version: '' })
  const [startError, setStartError] = useState(/** @type {any} */ (null))
  const [error, setError] = useState('')
  const [collapsed, setCollapsed] = useState(() => readCollapsed())
  const [membersOpen, setMembersOpen] = useState(false)
  const [page, setPage] = useState('') // '', 'settings', 'invite' or 'profile'
  const loaded = useRef(new Set()) // the rooms whose members and rooms were read
  const lang = browserLanguage()

  const rows = shownRows(state.list)
  const row = rows.find((r) => r.id === state.selected)
  const conv = convOf(state, state.selected)
  const joined = row?.joined === true && !row.error
  const pending = state.list.find((r) => !r.joined && !r.error)

  /** The members and the rooms of a conversation, which its engine told before the window listened. */
  const loadDirectory = (id) => {
    loaded.current.add(id)
    Promise.all([Members(id), Rooms(id)])
      .then(([members, rooms]) => dispatch({ type: 'directory', conv: id, members, rooms }))
      .catch((err) => setError(errorText(err)))
  }

  useEffect(() => {
    SetLanguage(lang).catch(() => {}) // the tray menu speaks the language of the window
    const offs = CHAT_EVENTS.map((name) => Events.On(name, (e) => dispatch({ type: 'event', name, payload: e.data, now: Date.now() })))
    offs.push(Events.On('chat:conversations', (e) => dispatch({ type: 'list', list: e.data })))
    Start()
      .then((info) => {
        setApp({ maxConversations: info.maxConversations, hasPins: info.hasPins, network: networkInfo(info.network), version: info.version ?? '' })
        dispatch({ type: 'list', list: info.conversations })
        if (info.first && shownRows(info.conversations).some((c) => c.id === info.first)) {
          dispatch({ type: 'select', id: info.first }) // the room that starts first is the one on screen
        }
      })
      .catch((err) => setStartError(err))
    return () => offs.forEach((off) => off())
  }, [])

  // A room reads its members and rooms once its engine has started: a room that waits for its turn has none to read yet.
  useEffect(() => {
    for (const c of shownRows(state.list)) {
      if (c.joined && !c.error && roomState(c) !== 'connecting' && !loaded.current.has(c.id)) {
        loadDirectory(c.id)
      }
    }
  }, [state.list])

  const toggleSidebar = () => {
    writeCollapsed(!collapsed)
    setCollapsed(!collapsed)
  }

  const select = (id) => {
    Focus(id).catch(() => {}) // a room that has not started yet starts before the others
    dispatch({ type: 'select', id })
    setPage('')
    setError('')
  }

  /** A conversation made or joined here: select it and read its members and rooms. */
  const added = (info) => {
    select(info.id)
    loadDirectory(info.id)
  }

  const send = () => {
    const text = conv.draft.trim()
    if (!text || !row) {
      return
    }
    const id = row.id
    Send(id, DEFAULT_ROOM, text)
      .then(() => {
        setError('')
        dispatch({ type: 'sent', conv: id, from: row.me, room: DEFAULT_ROOM, text, now: Date.now() })
      })
      .catch((err) => setError(errorText(err)))
  }

  const mine = conv.members.find((m) => m.key === row?.me)
  const admin = joined && (row.admin || isAdmin(conv.members, row.me)) // the list says it first, the member list once synced
  const shown = conv.messages.filter((m) => m.room === DEFAULT_ROOM) // the window shows each conversation's default room only
  const show = (name) => setPage((p) => (p === name ? '' : name))
  // The main column: a page, the join screen when there is no conversation, the reason one did not start, or the chat.
  const view = startError ? 'start-error' : page || (rows.length === 0 ? 'join' : row?.error ? 'failed' : 'chat')
  const relays = row ? conv.relays : state.relays
  const inRoom = joined && ['chat', 'invite', 'profile', 'settings'].includes(view)
  const me = joined ? { key: row.me, name: mine?.name, avatar: mine?.avatar } : null
  const noPin = joined ? conv.pin.pins.length === 0 : !app.hasPins

  return (
    <div className="app">
      <div className="titlebar" />
      <div className="body">
        {startError ? null : (
          <ConversationList
            lang={lang}
            rows={rows}
            selected={view === 'join' ? '' : state.selected}
            convs={state.convs}
            me={me}
            collapsed={collapsed}
            onToggle={toggleSidebar}
            max={app.maxConversations}
            count={state.list.length}
            pending={Boolean(pending)}
            noPin={noPin}
            noPinTitle={t(lang, joined ? 'chat.noPin' : 'conv.noPins')}
            settingsOpen={page === 'settings'}
            onSettings={() => show('settings')}
            onSelect={select}
            onAdded={added}
          />
        )}
        <div className="main">
          {inRoom ? (
            <ChatHeader lang={lang} row={row} conv={conv} admin={admin} page={page} onPage={show} membersOpen={membersOpen} onMembers={() => setMembersOpen((o) => !o)} />
          ) : null}
          <div className="main__body">
          <div className="stage">
          {view === 'settings' ? (
            <Page title={t(lang, 'settings.title')} onClose={() => setPage('')}>
              <SettingsPanel lang={lang} conv={joined ? row : null} pins={conv.pin.pins} relays={relays} network={app.network} version={app.version} onLeft={() => setPage('')} />
            </Page>
          ) : null}
          {view === 'invite' && admin ? (
            <Page title="Invite someone" onClose={() => setPage('')}>
              <InvitePanel lang={lang} conv={row.id} />
            </Page>
          ) : null}
          {view === 'profile' && joined ? (
            <Page title="Your profile" onClose={() => setPage('')}>
              <ProfilePanel conv={row.id} mine={mine} id={row.me} onDone={() => setPage('')} />
            </Page>
          ) : null}
          {view === 'join' ? <JoinPanel lang={lang} id={pending?.me ?? ''} empty={rows.length === 0} onJoined={added} /> : null}

          {view === 'start-error' ? (
            <div className="fault">
              <h2 className="fault__title">Kolo Chat could not start</h2>
              {startFaultKey(startError) && startFaultKey(startError) !== 'start.moveFolder' ? null : (
                <p className="fault__text">{errorText(startError)}</p>
              )}
              {startFaultKey(startError) ? (
                <>
                  <p className="fault__hint">{t(lang, startFaultKey(startError))}</p>
                  <p className="fault__hint"><code>{folderToMove(startError)}</code></p>
                </>
              ) : null}
            </div>
          ) : null}
          {view === 'failed' ? (
            <div className="fault">
              <h2 className="fault__title">{t(lang, 'conv.failed')}</h2>
              <p className="fault__text">{row.error}</p>
              {row.folder ? (
                <>
                  <p className="fault__hint">{t(lang, 'start.moveFolder')}</p>
                  <p className="fault__hint"><code>{row.folder}</code></p>
                </>
              ) : null}
            </div>
          ) : null}
          {view === 'chat' ? <MessageList lang={lang} messages={shown} members={conv.members} now={Date.now()} /> : null}

          <div className="notices">
          {joined ? netNotes(roomState(row), app.network).map((key) => <p key={key} className="note">{t(lang, key)}</p>) : null}
          {joined ? stateBanners(conv.state).map((b) => <p key={b.key} className="note">{t(lang, b.key, b.n)}</p>) : null}
          {error && view !== 'start-error' ? <p className="error">{error}</p> : null}
          {joined && conv.pin.error ? <p className="error">{conv.pin.error}</p> : null}
          {joined && !conv.pin.error && conv.pin.warning ? <p className="note">{conv.pin.warning}</p> : null}
          {joined && !conv.pin.error ? pinnedNotes(conv.pin.locked).map((note) => <p key={note} className="note">{note}</p>) : null}
          {startError ? null : failedRelays(relays).map((r) => (
            <p key={r.key} className="error">{t(lang, 'relay.label')} {r.addr}: {t(lang, relayStateKey(r))}</p>
          ))}

          </div>

          {view === 'chat' && joined ? (
            <Composer lang={lang} name={row.name} draft={conv.draft} onDraft={(text) => dispatch({ type: 'draft', conv: row.id, text })} onSend={send} />
          ) : null}
          </div>
          {inRoom && membersOpen ? (
            <MembersPanel lang={lang} conv={row.id} members={conv.members} me={row.me} requests={conv.requests}
              onRequestDone={(request) => dispatch({ type: 'request-done', conv: row.id, request })} onClose={() => setMembersOpen(false)} />
          ) : null}
          </div>
        </div>
      </div>
    </div>
  )
}

export default App
