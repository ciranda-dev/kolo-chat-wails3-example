import { useEffect, useRef, useState } from 'react'
import { Create } from "../bindings/github.com/ciranda-dev/kolo-chat-wails3-example/conversations.js"
import { JoinForm } from './Invite.jsx'
import { LinkIcon, PlusIcon, RoomPlusIcon } from './Icons.jsx'
import { Modal } from './Modal.jsx'
import { menuStep } from './focus.js'
import { t } from './i18n.js'

/** Turns whatever a rejected call threw into a string for the UI. */
const errorText = (err) => (err instanceof Error && err.message ? err.message : String(err))

/** The form of "Create a room": a name only you see, then Create. */
function CreateForm({ lang, onCreated, onCancel }) {
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const create = (ev) => {
    ev.preventDefault()
    setError('')
    Create(name)
      .then((info) => onCreated(info))
      .catch((err) => setError(errorText(err)))
  }
  return (
    <form className="addform" onSubmit={create}>
      <label className="panel__label">
        {t(lang, 'conv.name')}
        <input className="field" value={name} maxLength={32} placeholder={t(lang, 'conv.namePlaceholder')} onChange={(ev) => setName(ev.target.value)} autoComplete="off" />
      </label>
      <p className="note">{t(lang, 'conv.nameNote')}</p>
      <div className="panel__row">
        <button type="submit" className="btn">{t(lang, 'conv.create')}</button>
        <button type="button" className="btn btn--ghost" onClick={onCancel}>{t(lang, 'settings.leave.cancel')}</button>
      </div>
      {error ? <p className="error">{error}</p> : null}
    </form>
  )
}

/**
 * The "+" next to Rooms. It opens a small menu with two rows, Create a room and Join a room; a row opens a dialog with its
 * form. The menu closes on Escape and on a click outside, takes the arrow keys, Home, End and Enter, and gives the focus
 * back to the button. full and joinFull say that the bound of rooms is reached.
 */
export function AddRoom({ lang, full, joinFull, max, onAdded }) {
  const [open, setOpen] = useState('') // '', 'menu', 'create' or 'join'
  const [busy, setBusy] = useState(false)
  const button = useRef(null)
  const menu = useRef(null)

  const items = () => [...(menu.current?.querySelectorAll('[role="menuitem"]:not(:disabled)') ?? [])]
  const closeMenu = (refocus) => {
    setOpen('')
    if (refocus) {
      button.current?.focus()
    }
  }

  useEffect(() => {
    if (open !== 'menu') {
      return
    }
    ;(items()[0] ?? menu.current)?.focus()
    const away = (ev) => {
      if (!menu.current?.contains(ev.target) && !button.current?.contains(ev.target)) {
        setOpen('')
      }
    }
    document.addEventListener('mousedown', away)
    return () => document.removeEventListener('mousedown', away)
  }, [open])

  const onMenuKey = (ev) => {
    if (ev.key === 'Escape') {
      ev.preventDefault()
      ev.stopPropagation()
      closeMenu(true)
    } else if (ev.key === 'Tab') {
      closeMenu(false)
    } else {
      const list = items()
      const next = menuStep(list.indexOf(document.activeElement), list.length, ev.key)
      if (next >= 0) {
        ev.preventDefault()
        list[next].focus()
      }
    }
  }

  const added = (info) => {
    setOpen('')
    onAdded(info)
  }

  return (
    <div className="add">
      <button
        type="button"
        ref={button}
        className="iconbtn iconbtn--box"
        aria-label={t(lang, 'conv.add')}
        title={t(lang, 'conv.add')}
        aria-haspopup="menu"
        aria-expanded={open === 'menu'}
        onClick={() => setOpen(open === 'menu' ? '' : 'menu')}
        onKeyDown={(ev) => {
          if (ev.key === 'ArrowDown' && open !== 'menu') {
            ev.preventDefault()
            setOpen('menu')
          }
        }}
      >
        <PlusIcon />
      </button>
      {open === 'menu' ? (
        <div className="popover" role="menu" aria-label={t(lang, 'conv.add')} ref={menu} tabIndex={-1} onKeyDown={onMenuKey}>
          <button type="button" role="menuitem" className="choice" disabled={full} onClick={() => setOpen('create')}>
            <span className="choice__icon"><RoomPlusIcon size={20} /></span>
            <span className="choice__text"><span className="choice__title">{t(lang, 'conv.create.title')}</span><span className="choice__sub">{t(lang, 'conv.create.sub')}</span></span>
          </button>
          <button type="button" role="menuitem" className="choice" disabled={joinFull} onClick={() => setOpen('join')}>
            <span className="choice__icon"><LinkIcon size={20} /></span>
            <span className="choice__text"><span className="choice__title">{t(lang, 'conv.join.title')}</span><span className="choice__sub">{t(lang, 'conv.join.sub')}</span></span>
          </button>
          {full ? <p className="note popover__note">{t(lang, 'conv.max', max)}</p> : null}
        </div>
      ) : null}
      {open === 'create' ? (
        <Modal lang={lang} title={t(lang, 'conv.create.title')} returnFocus={button} onClose={() => setOpen('')}>
          <CreateForm lang={lang} onCreated={added} onCancel={() => setOpen('')} />
        </Modal>
      ) : null}
      {open === 'join' ? (
        <Modal lang={lang} title={t(lang, 'conv.join.title')} returnFocus={button} closable={!busy} onClose={() => setOpen('')}>
          <JoinForm lang={lang} bare onJoined={added} onBusy={setBusy} />
        </Modal>
      ) : null}
    </div>
  )
}
