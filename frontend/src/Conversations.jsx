import { AddRoom } from './AddRoom.jsx'
import { Avatar } from './Avatar.jsx'
import { GearIcon, SidebarIcon } from './Icons.jsx'
import { Mark } from './Mark.jsx'
import { labelFor, shortId } from './Profile.jsx'
import { DEFAULT_ROOM } from './state.js'
import { listStamp } from './times.js'
import { roomState, stateKey } from './network.js'
import { countKey, t } from './i18n.js'

/** The last message of the default room of a conversation as the list shows it: who, the text on one line, and when. */
export function lastMessage(conv, lang, now) {
  const m = conv?.messages?.findLast((x) => x.room === DEFAULT_ROOM)
  if (!m) {
    return null
  }
  const who = m.mine ? t(lang, 'chat.you') : labelFor(conv.members ?? [], m.from) || 'peer'
  const stamp = m.time > 0 ? listStamp(m.time, now, lang) : null
  return { text: `${who}: ${m.text.replace(/\s+/g, ' ').trim()}`, stamp: stamp ? (stamp.rel ? t(lang, `chat.${stamp.rel}`) : stamp.text) : '' }
}

/**
 * The sidebar, the full height of the window: the Kolo mark and the button that collapses it to a rail of avatars, the
 * list of rooms (it scrolls, the rest stays) with "+" beside the heading, a quiet line about the pin, and your card.
 * Each room shows its avatar, name, the last message and its time, an unread count, a mark when it did not start, and a dot while it connects, reconnects or cannot reach the network.
 */
export function ConversationList({ lang, rows, selected, convs, me, collapsed, onToggle, max, count, pending, noPin, noPinTitle, settingsOpen, onSettings, onSelect, onAdded }) {
  const now = Date.now()
  const full = max > 0 && count >= max
  const joinFull = full && !pending // a join takes the folder of the one that waits
  const toggle = t(lang, collapsed ? 'sidebar.expand' : 'sidebar.collapse')

  return (
    <nav className={`convs ${collapsed ? 'is-collapsed' : ''}`} id="rooms-panel" aria-label={t(lang, 'conv.title')}>
      <div className="convs__brand">
        <Mark className="convs__logo" size={28} />
        <span className="convs__name">kolo</span>
        <button type="button" className="iconbtn" aria-label={toggle} title={toggle} aria-expanded={!collapsed} aria-controls="rooms-panel" onClick={onToggle}>
          <SidebarIcon />
        </button>
      </div>
      <div className="convs__heading">
        <h2 className="sidebar__title">{t(lang, 'conv.title')}</h2>
        <AddRoom lang={lang} full={full} joinFull={joinFull} max={max} onAdded={onAdded} />
      </div>
      {rows.length === 0 ? <p className="note convs__empty">{t(lang, 'conv.empty')}</p> : null}
      <ul className="convs__list">
        {rows.map((r) => {
          const c = convs[r.id]
          const unread = c?.unread ?? 0
          const asking = (c?.requests ?? []).length > 0
          const last = lastMessage(c, lang, now)
          const state = roomState(r)
          return (
            <li key={r.id}>
              <button type="button" className={`conv ${r.id === selected ? 'is-active' : ''}`} aria-current={r.id === selected} aria-label={collapsed ? r.name : undefined} title={r.name} onClick={() => onSelect(r.id)}>
                <span className="conv__avatar">
                  <Avatar name={r.name} seed={r.id} size={44} />
                  {unread > 0 ? <span className="conv__dot" /> : null}
                </span>
                <span className="conv__text">
                  <span className="conv__top">
                    <span className="conv__name">{r.name}</span>
                    {r.error ? <span className="conv__failed" title={t(lang, 'conv.failed')}>!</span> : null}
                    {stateKey(state) ? <span className={`conv__net is-${state}`} role="img" title={t(lang, stateKey(state))} aria-label={t(lang, stateKey(state))} /> : null}
                    {asking ? <span className="conv__asking" title={t(lang, 'conv.requests')} /> : null}
                    <span className="conv__time">{last?.stamp}</span>
                  </span>
                  <span className="conv__bottom">
                    <span className="conv__preview">{last ? last.text : t(lang, 'conv.noMessages')}</span>
                    {unread > 0 ? <span className="conv__unread">{t(lang, countKey(lang, 'conv.unread', unread), unread)}</span> : null}
                  </span>
                </span>
              </button>
            </li>
          )
        })}
      </ul>
      {noPin ? <p className="convs__note" title={noPinTitle}>{t(lang, 'conv.noPinsShort')}</p> : null}
      <div className="me">
        {me ? (
          <>
            <Avatar name={me.name || ''} seed={me.key} glyph={me.avatar} size={40} />
            <span className="me__text">
              <span className="me__name">{me.name || shortId(me.key)}</span>
              <span className="me__key" title={me.key}>{shortId(me.key)}</span>
            </span>
          </>
        ) : <span className="me__text" />}
        <button type="button" className="iconbtn" aria-pressed={settingsOpen} aria-label={t(lang, 'settings.button')} title={t(lang, 'settings.button')} onClick={onSettings}>
          <GearIcon />
        </button>
      </div>
    </nav>
  )
}
