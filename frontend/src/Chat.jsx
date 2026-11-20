import { useEffect, useRef } from 'react'
import { Avatar } from './Avatar.jsx'
import { LockIcon, SendIcon, UserIcon, UsersIcon, LinkIcon } from './Icons.jsx'
import { labelFor } from './Profile.jsx'
import { clock, dayLabel } from './times.js'
import { groupMessages } from './groups.js'
import { headerState, roomState } from './network.js'
import { countKey, t } from './i18n.js'

/** The header of a room: its avatar and name, how many members and how many are online, and the buttons that exist. */
export function ChatHeader({ lang, row, conv, admin, page, onPage, membersOpen, onMembers }) {
  const members = conv.members.length
  const waiting = conv.requests.length
  const pending = headerState(roomState(row), conv.peers)
  return (
    <header className="chathead">
      <Avatar name={row.name} seed={row.id} size={40} />
      <div className="chathead__text">
        <h1 className="chathead__name" title={row.name}>{row.name}</h1>
        <p className="chathead__sub">
          {members > 0 ? t(lang, countKey(lang, 'chat.members', members), members) : null}
          {members > 0 ? ' · ' : null}
          <span className={conv.peers > 0 ? 'is-online' : pending ? 'is-pending' : ''}>{conv.peers > 0 ? t(lang, 'chat.online', conv.peers) : t(lang, pending || 'chat.nobodyOnline')}</span>
        </p>
      </div>
      <div className="chathead__actions">
        {admin ? (
          <button type="button" className="pill" aria-pressed={page === 'invite'} onClick={() => onPage('invite')}>
            <LinkIcon size={16} />
            <span className="pill__text">{t(lang, 'chat.invite')}</span>
          </button>
        ) : null}
        <button type="button" className="pill" aria-pressed={membersOpen} aria-expanded={membersOpen} aria-controls="members-panel" onClick={onMembers}>
          <UsersIcon size={16} />
          <span className="pill__text">{t(lang, 'chat.membersButton')}</span>
          {waiting > 0 ? <span className="pill__badge" title={t(lang, 'conv.requests')}>{waiting}</span> : null}
        </button>
        <button type="button" className="pill" aria-pressed={page === 'profile'} onClick={() => onPage('profile')}>
          <UserIcon size={16} />
          <span className="pill__text">{t(lang, 'chat.profile')}</span>
        </button>
      </div>
    </header>
  )
}

/** The text of a day chip: Today, Yesterday, or the date. */
export function dayText(lang, time, now) {
  const d = dayLabel(time, now, lang)
  return d.rel ? t(lang, `chat.${d.rel}`) : d.text
}

/**
 * The messages of a room, in day chips and groups: the author once above the first message of a group, a bubble per
 * message, and under the group the time of its last message and how it arrived. Mine are on the right.
 */
export function MessageList({ lang, messages, members, now }) {
  const bottom = useRef(null)
  useEffect(() => {
    bottom.current?.scrollIntoView({ block: 'end' })
  }, [messages])
  const items = groupMessages(messages)
  return (
    <div className="messages" role="log" aria-live="polite">
      {items.length === 0 ? <p className="empty">{t(lang, 'chat.empty')}</p> : null}
      {items.map((item) =>
        item.kind === 'day' ? (
          <div key={item.key} className="day"><span className="day__chip">{dayText(lang, item.time, now)}</span></div>
        ) : (
          <div key={item.key} className={`group ${item.mine ? 'is-mine' : 'is-theirs'}`}>
            <span className="group__from" title={item.from}>{item.mine ? t(lang, 'chat.you') : labelFor(members, item.from) || 'peer'}</span>
            <div className="group__bubbles">
              {item.msgs.map((msg) => <div key={msg.id} className="bubble">{msg.text}</div>)}
            </div>
            <span className="group__meta">
              {item.time > 0 ? clock(item.time, lang) : null}
              {item.vias.map((v) => <span key={v} className="group__via">{item.time > 0 ? ' · ' : ''}{t(lang, v === 'pin' ? 'chat.viaPin' : 'chat.viaPeer')}</span>)}
            </span>
          </div>
        ),
      )}
      <div ref={bottom} />
    </div>
  )
}

/** One rounded input and Send, and under them the line that says how the messages travel. */
export function Composer({ lang, name, draft, onDraft, onSend }) {
  return (
    <div className="composer">
      <form
        className="composer__form"
        onSubmit={(ev) => {
          ev.preventDefault()
          onSend()
        }}
      >
        <input
          className="composer__input"
          aria-label={t(lang, 'chat.message')}
          value={draft}
          onChange={(ev) => onDraft(ev.target.value)}
          type="text"
          placeholder={t(lang, 'chat.messageTo').replaceAll('{name}', name)}
          autoComplete="off"
        />
        <button type="submit" className="composer__send"><SendIcon size={16} />{t(lang, 'chat.send')}</button>
      </form>
      <p className="composer__note"><LockIcon size={13} />{t(lang, 'chat.encrypted')}</p>
    </div>
  )
}
