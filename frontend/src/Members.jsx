import { useState } from 'react'
import { Invites, RemoveMember, Revoke } from "../bindings/github.com/ciranda-dev/kolo-chat-wails3-example/conversations.js"
import { Avatar } from './Avatar.jsx'
import { CloseIcon } from './Icons.jsx'
import { Requests } from './Invite.jsx'
import { labelFor } from './Profile.jsx'
import { t } from './i18n.js'
import { list } from './lists.js'
import { canRemove, isAdmin, memberMarks } from './state.js'

/** Turns whatever a rejected call threw into a string for the UI. */
const errorText = (err) => (err instanceof Error && err.message ? err.message : String(err))

/**
 * The member list of the room conv, as the braid agrees it, in a panel beside the messages: the people waiting to be let
 * in, then each member with its marks. Admins remove members (never the settler), after a confirmation that says what
 * removal does not do. Changes the settler has not settled yet are marked pending.
 */
export function MembersPanel({ lang, conv, members, me, requests, onRequestDone, onClose }) {
  const [removing, setRemoving] = useState(/** @type {any} */ (null))
  const [revoke, setRevoke] = useState(true)
  const [error, setError] = useState('')
  const admin = isAdmin(members, me)

  const remove = async () => {
    const who = removing
    setRemoving(null)
    setError('')
    try {
      await RemoveMember(conv, who.key)
      if (revoke) {
        for (const i of list(await Invites(conv))) {
          if (i.state === 'active') {
            await Revoke(conv, i.id)
          }
        }
      }
    } catch (err) {
      setError(errorText(err))
    }
  }

  return (
    <aside className="members" id="members-panel" aria-label={t(lang, 'members.title')}>
      <div className="members__head">
        <h2 className="members__title">{t(lang, 'members.title')} <span className="members__count">{members.length}</span></h2>
        <button type="button" className="iconbtn" aria-label={t(lang, 'conv.close')} title={t(lang, 'conv.close')} onClick={onClose}><CloseIcon /></button>
      </div>
      <Requests lang={lang} conv={conv} requests={requests} onDone={onRequestDone} />
      <ul className="members__list">
        {members.map((m) => (
          <li key={m.key} className="member" title={m.key}>
            <Avatar name={m.name} seed={m.key} glyph={m.avatar} size={32} />
            <span className="member__text">
              <span className="member__name">{labelFor(members, m.key)}</span>
              <span className="member__tags">
                {m.seat ? <span className="member__tag">{t(lang, 'state.settler')}</span> : null}
                {memberMarks(m).map((k) => <span key={k} className="member__tag">{t(lang, k)}</span>)}
              </span>
            </span>
            {canRemove(members, me, m) ? (
              <button type="button" className="member__remove" onClick={() => setRemoving(m)}>{t(lang, 'state.remove')}</button>
            ) : null}
          </li>
        ))}
      </ul>
      {removing ? (
        <div className="confirm" role="alert">
          <p className="note">{t(lang, 'state.remove.confirm').replaceAll('{name}', labelFor(members, removing.key))}</p>
          <label className="check">
            <input type="checkbox" checked={revoke} onChange={(ev) => setRevoke(ev.target.checked)} />
            {t(lang, 'state.revokeInvites')}
          </label>
          <p className="note">{t(lang, 'state.multiUse')}</p>
          <div className="panel__row">
            <button type="button" className="btn btn--danger" onClick={remove}>{t(lang, 'state.remove')}</button>
            <button type="button" className="btn btn--ghost" onClick={() => setRemoving(null)}>{t(lang, 'settings.leave.cancel')}</button>
          </div>
        </div>
      ) : admin ? <p className="note">{t(lang, 'state.removeNote')}</p> : null}
      {error ? <p className="error">{error}</p> : null}
    </aside>
  )
}
