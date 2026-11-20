import { useState, useEffect } from 'react'
import { Join, CancelJoin, CreateInvite, Invites, Revoke, Approve, Deny } from "../bindings/github.com/ciranda-dev/kolo-chat-wails3-example/conversations.js"
import { Clipboard, Events } from "@wailsio/runtime"
import { Mark } from './Mark.jsx'
import { copyCode, shownCode } from './invites.js'
import { list } from './lists.js'
import { t } from './i18n.js'

/** @typedef {{ id: string, code?: string, expires: string, uses: number, used: number, state: string }} InviteInfo */
/** @typedef {{ request: number, invite: string, joiner: string, withdrawn: boolean }} JoinRequest */

const shortId = (id) => (id ? id.slice(0, 8) : '')

/** Turns whatever a rejected call threw into a string for the UI. */
function errorText(err) {
  if (err instanceof Error && err.message) {
    return err.message
  }
  return typeof err === 'string' ? err : String(err)
}

const browserCopy = (text) => navigator.clipboard.writeText(text)

/** Copies an invite code and says "Copied" for 2 seconds. */
function CopyButton({ code }) {
  const [state, setState] = useState('')
  useEffect(() => {
    if (!state) {
      return
    }
    const timer = setTimeout(() => setState(''), 2000)
    return () => clearTimeout(timer)
  }, [state])
  const copy = () => copyCode(code, Clipboard.SetText, browserCopy).then((ok) => setState(ok ? 'Copied' : 'Could not copy'))
  return (
    <>
      <button type="button" className="btn btn--ghost" onClick={copy}>Copy</button>
      <span className="invite__copied" aria-live="polite">{state}</span>
    </>
  )
}

const EXPIRIES = [
  { label: '10 minutes', seconds: 600 },
  { label: '1 hour', seconds: 3600 },
  { label: '1 day', seconds: 86400 },
  { label: '7 days', seconds: 604800 },
]

/**
 * Join with an invite: paste its code and give the room a name. In a modal it is the form alone; JoinPanel is also the
 * screen of an app with no room.
 */
export function JoinForm({ lang, empty = false, bare = false, onJoined, onBusy }) {
  const [code, setCode] = useState('')
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    return Events.On("chat:join", (e) => setStatus(e.data?.data?.status === 'failed' ? '' : (e.data?.data?.message ?? '')))
  }, [])

  const join = (ev) => {
    ev.preventDefault()
    if (!code.trim() || busy) {
      return
    }
    setBusy(true)
    onBusy?.(true)
    setError('')
    Join(code, name)
      .then((info) => onJoined(info))
      .catch((err) => setError(errorText(err)))
      .finally(() => {
        setBusy(false)
        onBusy?.(false)
        setStatus('')
      })
  }

  return (
    <form className="join__form" onSubmit={join}>
      {bare ? null : <h2 className="join__title">{t(lang, 'conv.join')}</h2>}
      {empty ? <p className="note">{t(lang, 'conv.empty')}</p> : null}
      <p className="note">Paste the invite code you received. An admin of the room approves your request: this waits for the answer.</p>
      <input
        className="field field--code"
        aria-label="Invite code"
        value={code}
        onChange={(ev) => setCode(ev.target.value)}
        placeholder="Invite code"
        autoComplete="off"
        spellCheck={false}
        disabled={busy}
      />
      <label className="panel__label join__name">
        {t(lang, 'conv.name')}
        <input className="field" value={name} maxLength={32} placeholder={t(lang, 'conv.namePlaceholder')} onChange={(ev) => setName(ev.target.value)} autoComplete="off" disabled={busy} />
      </label>
      <p className="note">{t(lang, 'conv.nameNote')}</p>
      <div className="panel__row">
        <button type="submit" className="btn" disabled={busy || !code.trim()}>Join</button>
        {!busy ? (
          <button type="button" className="btn btn--ghost" onClick={() => Clipboard.Text().then((s) => setCode(String(s ?? '').trim())).catch(() => {})}>
            Paste
          </button>
        ) : null}
        {busy ? <button type="button" className="btn btn--ghost" onClick={() => CancelJoin()}>Cancel</button> : null}
      </div>
      {status ? <p className="note">{status}</p> : null}
      {error ? <p className="error">{error}</p> : null}
    </form>
  )
}

/** The screen of an app with no room, and what Join with an invite shows: the form, and who you join as. */
export function JoinPanel({ lang, id = '', empty = false, onJoined }) {
  const [busy, setBusy] = useState(false)
  return (
    <div className="join">
    <JoinForm lang={lang} empty={empty} onJoined={onJoined} onBusy={setBusy} />
    <aside className="join__aside">
      <Mark className={`join__mark ${busy ? 'is-busy' : ''}`} size={120} />
      <div>
        <h3 className="eyebrow">You join as</h3>
        {id ? <p className="key"><span className="key__short">{shortId(id)}</span>{id.slice(8)}</p> : null}
        <p className="note">Each room gives you a key of its own on this device, made when you join. An admin sees its first 8 characters before approving, so you can tell them which request is yours.</p>
      </div>
    </aside>
    </div>
  )
}

/** Create an invite (expiry, uses) for the conversation conv, and see or revoke the ones made so far. An admin approves each request. */
export function InvitePanel({ lang, conv }) {
  const [seconds, setSeconds] = useState(86400)
  const [uses, setUses] = useState(1)
  const [invites, setInvites] = useState(/** @type {InviteInfo[]} */ ([]))
  const [codes, setCodes] = useState(/** @type {Record<string, string>} */ ({}))
  const [error, setError] = useState('')

  useEffect(() => {
    Invites(conv).then((got) => setInvites(list(got))).catch((err) => setError(errorText(err)))
    return Events.On("chat:invite", (e) => {
      const info = e.data?.conv === conv ? e.data.data : null
      if (info?.id) {
        setInvites((prev) => prev.map((i) => (i.id === info.id ? info : i)))
      }
    })
  }, [conv])

  const create = (ev) => {
    ev.preventDefault()
    setError('')
    CreateInvite(conv, { ttlSeconds: seconds, uses: Number(uses) || 1 })
      .then((info) => {
        setCodes((prev) => ({ ...prev, [info.id]: info.code }))
        setInvites((prev) => [...prev, info])
      })
      .catch((err) => setError(errorText(err)))
  }

  const revoke = (id) => {
    setError('')
    Revoke(conv, id).catch((err) => setError(errorText(err)))
  }

  return (
    <section className="panel split">
      <div className="split__col">
      <h3 className="eyebrow">New invite</h3>
      <form className="panel__row" onSubmit={create}>
        <label className="panel__label">
          Expires in
          <select className="field" value={seconds} onChange={(ev) => setSeconds(Number(ev.target.value))}>
            {EXPIRIES.map((x) => (
              <option key={x.seconds} value={x.seconds}>{x.label}</option>
            ))}
          </select>
        </label>
        <label className="panel__label">
          Uses
          <input
            className="field field--narrow"
            type="number"
            min="1"
            max="100"
            value={uses}
            onChange={(ev) => setUses(ev.target.value)}
          />
        </label>
        <button type="submit" className="btn">Create invite</button>
      </form>
      {error ? <p className="error">{error}</p> : null}
      <p className="note">Anyone with the code can ask to join, until it expires, runs out of uses or you revoke it. An admin approves each request.</p>
      <p className="note">{t(lang, 'state.approveNote')}</p>
      </div>
      <div className="split__col">
      <h3 className="eyebrow">Invites</h3>
      {invites.length === 0 ? <p className="note">No invites yet. Create one and its code shows here.</p> : null}
      <ul className="invites">
        {invites.map((i) => {
          const code = shownCode(i, codes)
          return (
            <li key={i.id} className="invite">
              <span className="invite__meta">
                {shortId(i.id)} · {i.state} · {i.used}/{i.uses} used
                {i.expires ? ` · expires ${new Date(i.expires).toLocaleString()}` : ''}
              </span>
              {i.state === 'active' ? (
                <button type="button" className="btn btn--ghost" onClick={() => revoke(i.id)}>Revoke</button>
              ) : null}
              {code ? (
                <div className="invite__code">
                  <input
                    className="field"
                    aria-label="Invite code"
                    readOnly
                    value={code}
                    onFocus={(ev) => ev.target.select()}
                  />
                  <CopyButton code={code} />
                </div>
              ) : null}
            </li>
          )
        })}
      </ul>
      {invites.length > 0 ? <p className="note">Give a code only to the person you invite. It cannot be shown again after the app restarts.</p> : null}
      </div>
    </section>
  )
}

/** The people waiting to be let in to the conversation conv, each approved as a member or as an admin, or denied. */
export function Requests({ lang, conv, requests, onDone }) {
  const [error, setError] = useState('')
  const answer = (call, request) => {
    setError('')
    call(request)
      .then(() => onDone?.(request))
      .catch((err) => setError(errorText(err)))
  }
  if (requests.length === 0) {
    return error ? <p className="error">{error}</p> : null
  }
  return (
    <div className="requests">
      {requests.map((r) => (
        <div key={r.request} className="request">
          <span className="request__who" title={r.joiner}>{shortId(r.joiner)} {t(lang, 'state.wantsToJoin')}</span>
          <button type="button" className="btn" onClick={() => answer((q) => Approve(conv, q, false), r.request)}>{t(lang, 'state.approveMember')}</button>
          <button type="button" className="btn btn--ghost" onClick={() => answer((q) => Approve(conv, q, true), r.request)}>{t(lang, 'state.approveAdmin')}</button>
          <button type="button" className="btn btn--ghost" onClick={() => answer((q) => Deny(conv, q), r.request)}>{t(lang, 'state.deny')}</button>
        </div>
      ))}
      {error ? <p className="error">{error}</p> : null}
    </div>
  )
}
