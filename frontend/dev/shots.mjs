// Screenshots of the mock, taken through Chrome's DevTools protocol, with layout checks.
// Start `npx vite --config dev/vite.config.js`, then:
//   node dev/shots.mjs <out.png> [base-url]     the README screenshot (1000 x 618 at 2x, dark)
//   node dev/shots.mjs --all <dir> [base-url]   every screen at three sizes, dark and light
// Both fail when a screen scrolls, cuts a message, or leaves a wide window mostly empty. KOLO_CHAT_SHOT_SIZES=1440x900 narrows the sizes.
// Needs Google Chrome; nothing to install.
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { openChrome, sleep } from './chrome.mjs'

const args = process.argv.slice(2)
const all = args[0] === '--all'
if (all) {
  args.shift()
}
const out = args[0]
const base = args[1] || 'http://127.0.0.1:9246'
if (!out) {
  console.error('usage: node dev/shots.mjs <out.png> [base-url]\n       node dev/shots.mjs --all <dir> [base-url]')
  process.exit(2)
}
const README_SIZE = [1000, 618] // the default size of the main window, in main.go
const SIZES = process.env.KOLO_CHAT_SHOT_SIZES
  ? process.env.KOLO_CHAT_SHOT_SIZES.split(',').map((s) => s.split('x').map(Number))
  : [[800, 600], README_SIZE, [1440, 900], [2000, 1240]]
const MESSAGES = 11 // the messages of the sample conversation in dev/mock.js, in its first room
const FILL = 0.9 // from 1440 px, the content of every screen spans at least this share of the window width

// Helpers the steps use inside the page.
const HELPERS = `
  window.press = (text) => [...document.querySelectorAll('button')].find((b) => b.textContent.trim().startsWith(text) || b.getAttribute('aria-label') === text).click()
  window.room = (name) => document.querySelector('.conv[title="' + name + '"]').click()
  window.add = () => document.querySelector('.add button').click()
  window.choose = (i) => document.querySelectorAll('[role=menuitem]')[i].click()
  window.type = (selector, text) => {
    const input = document.querySelector(selector)
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, text)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }
`

/** Each screen: the scene of dev/mock.js, the selector to wait for, and the steps that open it. */
const SCREENS = [
  { name: 'chat', scene: 'chat', rows: MESSAGES },
  { name: 'chat-one', scene: 'one', rows: MESSAGES },
  { name: 'chat-members', scene: 'busy', rows: MESSAGES, steps: [`press('Members')`], wait: '.members' },
  { name: 'chat-collapsed', scene: 'chat', rows: MESSAGES, steps: [`document.querySelector('[aria-controls="rooms-panel"]').click()`], wait: '.convs.is-collapsed' },
  { name: 'rooms-many', scene: 'many', rows: MESSAGES },
  { name: 'no-conversation', scene: 'none', wait: '[aria-label="Invite code"]' },
  { name: 'add-menu', scene: 'chat', rows: MESSAGES, steps: [`add()`], wait: '.popover' },
  { name: 'add-menu-collapsed', scene: 'chat', rows: MESSAGES, steps: [`document.querySelector('[aria-controls="rooms-panel"]').click()`, `add()`], wait: '.popover' },
  { name: 'add-create', scene: 'none', wait: '[aria-label="Invite code"]', steps: [`add()`, `choose(0)`], after: '.modal' },
  { name: 'add-join', scene: 'chat', rows: MESSAGES, steps: [`add()`, `choose(1)`], wait: '.modal' },
  { name: 'offline-room', scene: 'offline', rows: MESSAGES, wait: '.note' },
  { name: 'failed-conversation', scene: 'failed', rows: MESSAGES, steps: [`room('Notes')`], wait: '.fault' },
  { name: 'empty-room', scene: 'empty', wait: '.empty' },
  { name: 'invite', scene: 'chat', rows: MESSAGES, steps: [`room('Work')`, `press('Invite')`, `press('Create invite')`], wait: '.invite__code' },
  { name: 'invite-empty', scene: 'chat', rows: MESSAGES, steps: [`room('Work')`, `press('Invite')`] },
  { name: 'profile', scene: 'chat', rows: MESSAGES, steps: [`press('Profile')`] },
  { name: 'settings', scene: 'chat', rows: MESSAGES, steps: [`press('Settings')`], wait: '.network-note' },
  { name: 'settings-own-network', scene: 'own-network', rows: MESSAGES, steps: [`press('Settings')`], wait: '.pins__item' },
  { name: 'join', scene: 'join', wait: '[aria-label="Invite code"]' },
  { name: 'join-error', scene: 'join-error', wait: '[aria-label="Invite code"]', steps: [`type('[aria-label="Invite code"]', 'kolo-chat-invite:sample-code-for-the-mock-only')`, `document.querySelector('.join__form [type=submit]').click()`], after: '.error' },
  { name: 'join-waiting', scene: 'join-wait', wait: '[aria-label="Invite code"]', steps: [`type('[aria-label="Invite code"]', 'kolo-chat-invite:sample-code-for-the-mock-only')`, `document.querySelector('.join__form [type=submit]').click()`] },
  { name: 'join-settings', scene: 'join', wait: '[aria-label="Invite code"]', steps: [`press('Settings')`] },
  { name: 'start-error', scene: 'start-error', wait: '.fault' },
  { name: 'other-layout', scene: 'other-layout', wait: '.fault' },
]

const { send, evaluate, until, close } = await openChrome(['--hide-scrollbars', '--lang=en-US', '--force-device-scale-factor=2'])

// What is wrong with the layout on screen: scrolling, a cut message, text against an edge, or, in a wide
// window, content that leaves more than a tenth of the width empty.
const check = (CUT) => `(() => {
  const d = document.documentElement
  const found = []
  if (d.scrollWidth > innerWidth) found.push('page scrolls sideways')
  if (d.scrollHeight > innerHeight) found.push('page scrolls down')
  const m = document.querySelector('.messages')
  if (${CUT} && m && m.offsetParent && [...m.querySelectorAll('.group')].slice(-1).some((r) => { const a = r.getBoundingClientRect(); const b = m.getBoundingClientRect(); return a.top < b.top || a.bottom > b.bottom })) found.push('the newest message is cut')
  const leaves = [...document.querySelectorAll('.app *')].filter((el) => {
    if (el.closest('.titlebar') || !el.getClientRects().length || getComputedStyle(el).visibility === 'hidden') return false
    return /^(INPUT|SELECT|BUTTON|IMG)$/.test(el.tagName) || [...el.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim())
  })
  let left = Infinity, right = -Infinity
  for (const el of leaves) {
    const box = el.getBoundingClientRect()
    if (box.width === 0 || box.height === 0) continue
    const cs = getComputedStyle(el)
    const r = { left: box.left + parseFloat(cs.paddingLeft), right: box.right - parseFloat(cs.paddingRight) }
    if (/^(INPUT|SELECT|BUTTON|IMG)$/.test(el.tagName)) { r.left = box.left; r.right = box.right }
    left = Math.min(left, r.left); right = Math.max(right, r.right)
    if (r.left < 8 || r.right > innerWidth - 8) found.push('no inset: ' + (el.className || el.tagName) + ' "' + el.textContent.trim().slice(0, 24) + '"')
  }
  const share = (right - left) / innerWidth
  if (innerWidth >= 1440 && share < ${FILL}) found.push('content spans ' + Math.round(share * 100) + '% of the width, want ' + ${FILL * 100} + '%')
  return found.join(', ')
})()`

/** Opens one screen at one size and theme, checks it and saves it. */
async function shoot(screen, [width, height], theme, file) {
  await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 2, mobile: false })
  await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }, { name: 'prefers-color-scheme', value: theme }] })
  await send('Page.navigate', { url: `${base}/?scene=${screen.scene}` })
  const label = `${screen.name} ${width}x${height} ${theme}`
  if (screen.rows && !(await until(`document.querySelectorAll('.bubble').length >= ${screen.rows}`))) {
    problems.push(`${label}: ${await evaluate(`document.querySelectorAll('.bubble').length`)} messages on screen, want ${screen.rows}`)
  }
  if (screen.wait && !screen.steps && !(await until(`!!document.querySelector(${JSON.stringify(screen.wait)})`))) {
    problems.push(`${label}: ${screen.wait} never showed`)
  }
  if (screen.steps) {
    if (screen.name.startsWith('join') || screen.after) {
      await until(`!!document.querySelector(${JSON.stringify(screen.wait)})`)
    }
    await evaluate(HELPERS)
    for (const step of screen.steps) {
      await evaluate(step)
      await sleep(150)
    }
    const shown = screen.after ?? (screen.name.startsWith('join') ? null : screen.wait)
    if (shown && !(await until(`!!document.querySelector(${JSON.stringify(shown)})`))) {
      problems.push(`${label}: ${shown} never showed`)
    }
  }
  await evaluate(`document.activeElement?.blur()`)
  await sleep(300)
  const wrong = await evaluate(check(screen.name === 'chat'))
  if (wrong) {
    problems.push(`${label}: ${wrong}`)
  }
  const shot = await send('Page.captureScreenshot', { format: 'png' })
  writeFileSync(file, Buffer.from(shot.data, 'base64'))
}

const problems = []
let count = 0
try {
  await send('Page.enable')
  await send('Page.addScriptToEvaluateOnNewDocument', { source: `try { localStorage.removeItem('kolo-chat.sidebar') } catch {}` }) // each screen starts with the sidebar open
  if (all) {
    mkdirSync(out, { recursive: true })
    for (const screen of SCREENS) {
      for (const size of SIZES) {
        for (const theme of ['dark', 'light']) {
          await shoot(screen, size, theme, join(out, `${screen.name}-${size[0]}x${size[1]}-${theme}.png`))
          count++
        }
      }
    }
  } else {
    await shoot(SCREENS[0], README_SIZE, 'dark', out)
    count++
  }
} finally {
  await close()
}
console.log(`${count} ${count === 1 ? 'screenshot' : 'screenshots'} in ${out}`)
if (problems.length > 0) {
  console.log(problems.join('\n'))
  process.exitCode = 1
}
