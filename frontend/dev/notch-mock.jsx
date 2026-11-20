import { useState } from 'react'
import { Notch } from '../src/notch/Notch.jsx'

// Development only: every state of the notch in a browser, at its real size. Open /notch?mock with `npm run dev`.
const base = { supported: true, enabled: true, showText: true, hasMessage: true, name: 'Ana Souza', avatar: '🦊', room: 'general', preview: 'Lunch at one? I booked the table by the window.', unread: 1, peers: 3, pin: 'ok', locked: 0 }

const states = {
  'Idle': { ...base, hasMessage: false, name: '', avatar: '', room: '', preview: '', unread: 0 },
  'Message': base,
  'Long text': { ...base, name: 'A very long display name of forty chars..', preview: 'This preview was cut by the app at seventy-two characters, so it ends h…', unread: 12 },
  'Preview off': { ...base, showText: false, preview: '', unread: 2 },
  'Initial, no avatar': { ...base, name: 'Sam #aa11', avatar: 'S', room: 'dev' },
  'Unknown sender': { ...base, name: 'aa11aa11', avatar: '' },
  'Read': { ...base, unread: 0 },
  'No peers': { ...base, peers: 0, pin: 'unknown' },
  'Pin error': { ...base, pin: 'error' },
  'Pin waiting': { ...base, pin: 'waiting', locked: 4, unread: 128 },
}

export function MockNotch() {
  const asked = new URLSearchParams(window.location.search).get('mock') // /notch?mock=Pin%20waiting
  const [name, setName] = useState(asked in states ? asked : 'Message')
  const [lang, setLang] = useState('en')
  const [log, setLog] = useState('')
  return (
    <div className="mock">
      <div className="mock__frame">
        <Notch state={states[name]} lang={lang} onOpen={() => setLog('Open chat')} onHide={() => setLog('Hide')} />
      </div>
      <p className="mock__row">
        {Object.keys(states).map((s) => (
          <button key={s} type="button" aria-pressed={s === name} onClick={() => setName(s)}>{s}</button>
        ))}
      </p>
      <p className="mock__row">
        {['en', 'es', 'pt'].map((l) => (
          <button key={l} type="button" aria-pressed={l === lang} onClick={() => setLang(l)}>{l}</button>
        ))}
        <span>{log}</span>
      </p>
    </div>
  )
}
