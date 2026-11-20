import { useEffect, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { SetEnabled, SetShowText, State } from "../bindings/github.com/ciranda-dev/kolo-chat-wails3-example/notchservice.js"
import { CancelLeave, Leave, PlanLeave, Rename } from "../bindings/github.com/ciranda-dev/kolo-chat-wails3-example/conversations.js"
import { t } from './i18n.js'
import { pinProblem, shortKey } from './pins.js'
import { handoverAdmins, leaveNotes, leaveStep, leaveTarget, leaveToken } from './leave.js'
import { list } from './lists.js'
import { relayStateKey } from './relays.js'
import { networkKey } from './network.js'

/**
 * The settings of the app: whether a message shows in the notch window, and with its text or not. Each change
 * sends its own setting only, and the panel follows "notch:state", so a change from the tray menu shows here.
 * It also lists the pins in use, from "chat:pin": they are set at start, by flag, variable or file, or by the invite,
 * and the connection relays with their state, from "chat:relays", says which network the app joins (network) and which release it is (version).
 * With a conversation selected (conv, its row in the list) it shows our kolo-id in it, renames it and leaves it;
 * onLeft runs once it is gone from this computer.
 */
export function SettingsPanel({ lang, conv = null, pins = [], relays = { from: '', relays: [] }, network = { mode: 'public', nodes: [] }, version = '', onLeft = () => {} }) {
  const [state, setState] = useState(/** @type {{ supported: boolean, enabled: boolean, showText: boolean } | null} */ (null))
  const [error, setError] = useState('')
  const failed = (err) => setError(String(err?.message ?? err))

  useEffect(() => {
    State().then(setState).catch(failed)
    return Events.On('notch:state', (e) => e.data && setState(e.data))
  }, [])

  const change = (set, on) => {
    setError('')
    set(on).catch(failed)
  }

  const pinList = <div className="split__col"><NetworkSection lang={lang} info={network} /><PinList lang={lang} pins={pins} /><RelayList lang={lang} status={relays} /><About lang={lang} version={version} /></div>
  const title = <h3 className="eyebrow">{t(lang, 'settings.notch.title')}</h3>
  const leave = conv ? <><ConversationSection key={`name-${conv.id}`} lang={lang} conv={conv} /><LeaveSection key={`leave-${conv.id}`} lang={lang} conv={conv.id} onLeft={onLeft} /></> : null
  if (!state) {
    return (
      <section className="panel split">
        <div className="split__col">{title}{error ? <p className="error">{error}</p> : null}{leave}</div>
        {pinList}
      </section>
    )
  }
  return (
    <section className="panel split">
      <div className="split__col">
      {title}
      <label className="check">
        <input type="checkbox" checked={state.enabled} disabled={!state.supported} onChange={(ev) => change(SetEnabled, ev.target.checked)} />
        {t(lang, 'settings.notch')}
      </label>
      <label className="check">
        <input type="checkbox" checked={state.showText} disabled={!state.supported || !state.enabled} onChange={(ev) => change(SetShowText, ev.target.checked)} />
        {t(lang, 'settings.preview')}
      </label>
      <p className="note">{t(lang, state.supported ? 'settings.note' : 'settings.unsupported')}</p>
      {error ? <p className="error">{error}</p> : null}
      {leave}
      </div>
      {pinList}
    </section>
  )
}

/** Our kolo-id in the conversation, which a pin's -owners or a relay's allow list names, and its name, which Rename changes. */
function ConversationSection({ lang, conv }) {
  const [name, setName] = useState(conv.name)
  const [error, setError] = useState('')
  const rename = (ev) => {
    ev.preventDefault()
    setError('')
    Rename(conv.id, name).catch((err) => setError(String(err?.message ?? err)))
  }
  return (
    <>
      <h3 className="eyebrow">{t(lang, 'conv.me')}</h3>
      <p className="key">{conv.me}</p>
      <form className="panel__row" onSubmit={rename}>
        <label className="panel__label panel__label--grow">
          {t(lang, 'conv.name')}
          <input className="field" value={name} maxLength={32} onChange={(ev) => setName(ev.target.value)} autoComplete="off" />
        </label>
        <button type="submit" className="btn" disabled={!name.trim() || name.trim() === conv.name}>{t(lang, 'conv.rename')}</button>
      </form>
      <p className="note">{t(lang, 'conv.nameNote')}</p>
      {error ? <p className="error">{error}</p> : null}
    </>
  )
}

/**
 * Leave conversation: the plan from Go first, with every path it deletes, and Leave only after a confirm. The founder
 * may hand settlement over to another admin; Leave then waits, with Cancel, until that admin took over.
 */
function LeaveSection({ lang, conv, onLeft }) {
  const [state, setState] = useState({ step: 'idle' })
  const failed = (err) => setState((s) => leaveStep(s, { type: 'error', error: String(err?.message ?? err) }))

  const plan = () => PlanLeave(conv).then((p) => setState((s) => leaveStep(s, { type: 'plan', plan: p }))).catch(failed)
  const confirm = (to = '') => {
    const next = leaveStep(state, { type: 'confirm', to })
    const token = leaveToken(next)
    if (!token) {
      return
    }
    setState(next)
    Leave(conv, token, leaveTarget(next))
      .then(() => {
        setState({ step: 'idle' })
        onLeft()
      })
      .catch(failed)
  }

  const title = <h3 className="eyebrow">{t(lang, 'settings.leave.title')}</h3>
  if (state.step !== 'confirm' && state.step !== 'leaving') {
    return (
      <>
        {title}
        <button type="button" className="btn btn--ghost" onClick={plan}>{t(lang, 'settings.leave.button')}</button>
        <p className="note">{t(lang, 'settings.leave.note')}</p>
        {state.step === 'failed' ? <p className="error">{state.error}</p> : null}
      </>
    )
  }
  const leaving = state.step === 'leaving'
  const target = handoverAdmins(state.plan).find((a) => a.key === leaveTarget(state))
  const nameOf = (a) => a.name || a.key.slice(0, 8)
  return (
    <>
      {title}
      <div className="leave" role="alert">
        {leaveNotes(state.plan).map((key) => <p key={key} className="note">{t(lang, key)}</p>)}
        <p className="leave__label">{t(lang, 'settings.leave.delete')}</p>
        <ul className="leave__paths">{list(state.plan.delete).map((path) => <li key={path}><code>{path}</code></li>)}</ul>
        <p className="leave__label">{t(lang, 'settings.leave.keep')}</p>
        <ul className="leave__paths">{list(state.plan.keep).map((path) => <li key={path}><code>{path}</code></li>)}</ul>
        {target ? <p className="note">{t(lang, 'state.handoverWait').replace('{name}', nameOf(target))}</p> : null}
        <div className="leave__actions">
          {handoverAdmins(state.plan).map((a) => (
            <button key={a.key} type="button" className="btn" disabled={leaving} onClick={() => confirm(a.key)}>
              {t(lang, 'state.handover').replace('{name}', nameOf(a))}
            </button>
          ))}
          <button type="button" className="btn btn--danger" disabled={leaving} onClick={() => confirm()}>
            {t(lang, leaving && !target ? 'settings.leave.leaving' : 'settings.leave.confirm')}
          </button>
          {target ? (
            <button type="button" className="btn btn--ghost" onClick={() => CancelLeave(conv).catch(failed)}>{t(lang, 'settings.leave.cancel')}</button>
          ) : (
            <button type="button" className="btn btn--ghost" disabled={leaving} onClick={() => setState((s) => leaveStep(s, { type: 'cancel' }))}>
              {t(lang, 'settings.leave.cancel')}
            </button>
          )}
        </div>
      </div>
    </>
  )
}

/** The network the app joins, with the entry nodes of your own network (never the public ones), and the other ways to choose one. */
function NetworkSection({ lang, info }) {
  return (
    <>
      <h3 className="panel__title pins__title">{t(lang, 'settings.network')}</h3>
      <p className="note network-note">{t(lang, networkKey(info))}</p>
      {info.mode === 'own' ? (
        <ul className="pins">
          {info.nodes.map((n) => (
            <li key={n} className="pins__item"><span className="pins__addr">{n}</span></li>
          ))}
        </ul>
      ) : null}
      <p className="note">{t(lang, 'settings.network.note')}</p>
    </>
  )
}

/** The release of the app, "dev" for a build that is not one. */
function About({ lang, version }) {
  return version ? (
    <>
      <h3 className="panel__title pins__title">{t(lang, 'settings.about')}</h3>
      <p className="note">{`${t(lang, 'settings.version')} ${version}`}</p>
    </>
  ) : null
}

/** The connection relays, each with its address, the start of its key and its state, and where they were set. */
function RelayList({ lang, status }) {
  return (
    <>
      <h3 className="panel__title pins__title">{t(lang, 'settings.relays')}</h3>
      {status.relays.length === 0 ? <p className="note">{t(lang, 'settings.relays.none')}</p> : null}
      <ul className="pins">
        {status.relays.map((r) => (
          <li key={r.key} className="pins__item" title={r.key}>
            <span className="pins__addr">{r.addr}</span> <code>{shortKey(r.key)}</code>{' '}
            <span className={r.state === 'failed' ? 'error' : 'note'}>{t(lang, relayStateKey(r))}</span>
          </li>
        ))}
      </ul>
      {status.from ? <p className="note">{t(lang, 'settings.relays.from')}: <code>{status.from}</code></p> : null}
      <p className="note">{t(lang, 'settings.relays.note')}</p>
    </>
  )
}

/** The pins in use, each with its address, the start of its key and whether it works. */
function PinList({ lang, pins }) {
  return (
    <>
      <h3 className="panel__title pins__title">{t(lang, 'settings.pins')}</h3>
      {pins.length === 0 ? <p className="note">{t(lang, 'settings.pins.none')}</p> : null}
      <ul className="pins">
        {pins.map((p) => {
          const why = pinProblem(p)
          return (
            <li key={p.key} className="pins__item" title={p.key}>
              <span className="pins__addr">{p.addr}</span> <code>{shortKey(p.key)}</code>{' '}
              <span className={why ? 'error' : 'note'}>{why || t(lang, 'settings.pins.ok')}</span>
            </li>
          )
        })}
      </ul>
      <p className="note">{t(lang, 'settings.pins.note')}</p>
    </>
  )
}
