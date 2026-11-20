import React from 'react'
import ReactDOM from 'react-dom/client'
import { browserLanguage } from '../i18n.js'
import { housingOf, showHousing } from './housing.js'
import { CrashBoundary, CrashPane } from '../Crash.jsx'
import { installCrashHandlers, report } from '../crash.js'
import './notch.css'

installCrashHandlers()
document.documentElement.lang = browserLanguage()
showHousing(document.documentElement, housingOf(window.location.search))
const mock = import.meta.env.DEV && new URLSearchParams(window.location.search).has('mock')
const page = mock
  ? import('../../dev/notch-mock.jsx').then((m) => m.MockNotch)
  : import('./live.jsx').then((m) => m.LiveNotch)

const root = ReactDOM.createRoot(document.getElementById('root'))
page.then(
  (Page) => {
    root.render(
      <React.StrictMode>
        <CrashBoundary compact>
          <Page />
        </CrashBoundary>
      </React.StrictMode>,
    )
  },
  (err) => root.render(<CrashPane text={report(err)} compact />),
)
