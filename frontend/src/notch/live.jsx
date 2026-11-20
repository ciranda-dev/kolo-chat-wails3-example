import { useEffect, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { State, Hold, Release, Hide, OpenChat } from '../../bindings/github.com/ciranda-dev/kolo-chat-wails3-example/notchservice.js'
import { browserLanguage } from '../i18n.js'
import { NOTCH_EVENT, emptyState } from './model.js'
import { Notch } from './Notch.jsx'

/** The notch window in the app: its state comes from NotchService, and from nowhere else. */
export function LiveNotch() {
  const [state, setState] = useState(emptyState)
  const lang = browserLanguage()

  useEffect(() => {
    State().then((s) => s && setState(s)).catch(() => {})
    const off = Events.On(NOTCH_EVENT, (e) => e.data && setState(e.data))
    const onKey = (ev) => {
      if (ev.key === 'Escape') {
        Hide()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => {
      off()
      window.removeEventListener('keydown', onKey)
    }
  }, [])

  return <Notch state={state} lang={lang} onOpen={() => OpenChat()} onHide={() => Hide()} onHold={() => Hold()} onRelease={() => Release()} />
}
