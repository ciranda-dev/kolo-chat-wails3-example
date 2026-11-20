import { Component, useEffect, useState } from 'react'
import { Clipboard } from '@wailsio/runtime'
import { browserLanguage, t } from './i18n.js'
import { copyCode } from './invites.js'
import { onCrash, report } from './crash.js'
import { reportParts, reportText } from './report.js'
import './crash.css'

const browserCopy = (text) => navigator.clipboard.writeText(text)

/**
 * The error of a window on the whole window, with a Copy button, never a blank window. onDismiss when the window may go
 * on; compact for the notch window.
 */
export function CrashPane({ text, onDismiss, compact = false }) {
  const lang = browserLanguage()
  const [copied, setCopied] = useState('')
  const copy = () => copyCode(text, Clipboard.SetText, browserCopy).then((ok) => setCopied(ok ? 'crash.copied' : 'crash.copyFailed'))
  return (
    <div className={`crash ${compact ? 'is-compact' : ''}`} role="alert" lang={lang}>
      <h1 className="crash__title">{t(lang, 'crash.title')}</h1>
      <p className="crash__note">{t(lang, 'crash.note')}</p>
      <pre className="crash__text" tabIndex={0}>{text}</pre>
      <div className="crash__actions">
        <button type="button" className="crash__btn" onClick={copy} autoFocus>{t(lang, 'crash.copy')}</button>
        {onDismiss ? <button type="button" className="crash__btn is-ghost" onClick={onDismiss}>{t(lang, 'crash.dismiss')}</button> : null}
        <span className="crash__status" aria-live="polite">{copied ? t(lang, copied) : ''}</span>
      </div>
    </div>
  )
}

/** Shows an error that happened outside a render (an event, a promise) over the window, which may go on. */
function WindowErrors({ compact, children }) {
  const [text, setText] = useState('')
  useEffect(() => onCrash(setText), [])
  return (
    <>
      {children}
      {text ? <CrashPane text={text} compact={compact} onDismiss={() => setText('')} /> : null}
    </>
  )
}

/** The root of a page: a render that throws shows the error pane instead of nothing, and the error goes to the log. */
export class CrashBoundary extends Component {
  constructor(props) {
    super(props)
    this.state = { text: '' }
  }

  static getDerivedStateFromError(error) {
    return { text: reportText(reportParts(error)) }
  }

  componentDidCatch(error, info) {
    this.setState({ text: report(error, info?.componentStack ?? '') })
  }

  render() {
    if (this.state.text) {
      return <CrashPane text={this.state.text} compact={this.props.compact} />
    }
    return <WindowErrors compact={this.props.compact}>{this.props.children}</WindowErrors>
  }
}
