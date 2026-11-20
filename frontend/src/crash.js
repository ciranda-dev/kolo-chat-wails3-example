import { ReportError } from '../bindings/github.com/ciranda-dev/kolo-chat-wails3-example/notchservice.js'
import { reportParts, reportText } from './report.js'

/** Sends the error and its stacks to the terminal log through Go, and returns the text for the pane. */
export function report(error, componentStack = '') {
  const parts = reportParts(error, componentStack)
  try {
    ReportError(parts.message, parts.stack).catch(() => {})
  } catch {
    // without the runtime there is no log, and the pane still shows
  }
  return reportText(parts)
}

const listeners = new Set()

/** Calls fn with the text of every error the window did not handle, until the returned function is called. */
export function onCrash(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

/** Reports and shows every error that no code caught: window.onerror and unhandled promise rejections. */
export function installCrashHandlers() {
  const crash = (error) => {
    const text = report(error)
    listeners.forEach((fn) => fn(text))
  }
  window.addEventListener('error', (ev) => crash(ev.error ?? ev.message))
  window.addEventListener('unhandledrejection', (ev) => crash(ev.reason))
}
