import { useState } from 'react'
import { SetProfile } from "../bindings/github.com/ciranda-dev/kolo-chat-wails3-example/conversations.js"

/** @typedef {{ key: string, name: string, avatar: string, admin: boolean, seat: boolean, pending: boolean, flagged: boolean, mismatch: boolean }} Member */

export const shortId = (id) => (id ? id.slice(0, 8) : '')

/** Turns whatever a rejected call threw into a string for the UI. */
function errorText(err) {
  if (err instanceof Error && err.message) {
    return err.message
  }
  return typeof err === 'string' ? err : String(err)
}

/**
 * The name to show for a member: its profile name, or the short key until its profile has been read. A name is
 * not an identity, so two members with the same name get the start of their keys too.
 * @param {Member[]} members
 * @param {string} key
 */
export function labelFor(members, key) {
  const m = members.find((x) => x.key === key)
  if (!m || !m.name) {
    return shortId(key)
  }
  const twin = members.some((x) => x.key !== key && x.name === m.name)
  return twin ? `${m.name} #${key.slice(0, 4)}` : m.name
}

/** Change your name and avatar in the conversation conv. The avatar is a short reference, such as an emoji, not an image. */
export function ProfilePanel({ conv, mine, id = '', onDone }) {
  const [name, setName] = useState(mine?.name ?? '')
  const [avatar, setAvatar] = useState(mine?.avatar ?? '')
  const [error, setError] = useState('')

  const save = (ev) => {
    ev.preventDefault()
    setError('')
    SetProfile(conv, name, avatar)
      .then(() => onDone())
      .catch((err) => setError(errorText(err)))
  }

  return (
    <section className="panel split">
      <div className="split__col">
      <h3 className="eyebrow">Name and avatar</h3>
      <form className="panel__row" onSubmit={save}>
        <label className="panel__label panel__label--grow">
          Name
          <input className="field" value={name} maxLength={40} onChange={(ev) => setName(ev.target.value)} autoComplete="off" />
        </label>
        <label className="panel__label">
          Avatar
          <input className="field field--narrow" value={avatar} maxLength={16} onChange={(ev) => setAvatar(ev.target.value)} placeholder="🦊" autoComplete="off" />
        </label>
        <button type="submit" className="btn" disabled={!name.trim()}>Save</button>
      </form>
      {error ? <p className="error">{error}</p> : null}
      </div>
      <div className="split__col">
        <h3 className="eyebrow">How others see you</h3>
        <div className="preview">
          <span className="avatar avatar--big" aria-hidden="true">{avatar || (name.trim() || '?').slice(0, 1).toUpperCase()}</span>
          <span className="preview__name">{name.trim() || shortId(mine?.key || id)}</span>
        </div>
        <h3 className="eyebrow">Your key</h3>
        <p className="key">{mine?.key || id}</p>
        <p className="note">A name is a label, and two people can pick the same one. Your key is what tells you apart: it stays the same when you change your name. Each room has its own key and its own profile.</p>
      </div>
    </section>
  )
}
