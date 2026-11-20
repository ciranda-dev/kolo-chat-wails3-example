import { notchModel } from './model.js'

/** The Kolo ring: a thin circle with the accent dot on top while there is something unread. */
function Ring({ unread, children }) {
  return (
    <span className={`ring ${unread ? 'is-unread' : ''}`} aria-hidden="true">
      <svg viewBox="0 0 26 26" focusable="false">
        <circle className="ring__line" cx="13" cy="13" r="10" />
        <circle className="ring__top" cx="13" cy="3" r="2.4" />
        {children ? null : (
          <>
            <circle className="ring__dot" cx="22.7" cy="15.8" r="2.4" />
            <circle className="ring__dot" cx="7" cy="21.2" r="2.4" />
          </>
        )}
      </svg>
      {children ? <span className="ring__text">{children}</span> : null}
    </span>
  )
}

/**
 * What the notch window shows in its 660 x 92 area, below the camera housing (see notch.css).
 * @param {{ state: object, lang: string, onOpen: () => void, onHide: () => void, onHold?: () => void, onRelease?: () => void }} props
 */
export function Notch({ state, lang, onOpen, onHide, onHold, onRelease }) {
  const m = notchModel(state, lang)
  return (
    <main className="notch" aria-label={m.label} onPointerEnter={onHold} onPointerLeave={onRelease}>
      <div className="notch__top">
        <span className="notch__brand">kolo</span>
        {m.room ? <span className="notch__room">{m.room}</span> : null}
        <span className="notch__gap" />
        <span className={`chip ${m.live ? 'is-live' : ''}`}>
          <span className="chip__dot" aria-hidden="true" />
          {m.peers}
        </span>
        {m.pin ? <span className={`chip is-pin-${m.pinState}`}>{m.pin}</span> : null}
        <button type="button" className="icon-btn" onClick={onHide} aria-label={m.hide} title={m.hide}>
          <svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><path d="m3.5 9.5 4.5-4 4.5 4" /></svg>
        </button>
      </div>

      <div className="notch__main">
        <Ring unread={m.unread !== ''}>{m.avatar}</Ring>
        <div className="notch__message" role="status" aria-live="polite">
          {m.name ? <span className="notch__name">{m.name}</span> : null}
          <span className={`notch__preview ${m.lineIsText ? '' : 'is-hidden-text'}`}>{m.line}</span>
        </div>
        {m.unread ? <span className="notch__unread">{m.unread}</span> : null}
        <button type="button" className="open-btn" onClick={onOpen}>{m.open}</button>
      </div>
    </main>
  )
}
