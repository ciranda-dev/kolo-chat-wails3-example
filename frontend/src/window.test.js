import test from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { hasChrome, openChrome, sleep } from '../dev/chrome.mjs'

// Both windows, served with dev/mock.js in place of Go (loopback only) in a headless Chrome: a null list in any event
// or method result never leaves a blank window, and an error that nobody caught shows the error pane, which fills the
// window, reaches ReportError and copies. The window has one list of rooms with no inner level; messages group by author
// and by five minutes with day chips; the sidebar collapses and remembers it; the add menu and its dialog take the
// keyboard and give the focus back; Leave room shows its plan and leaves only after the confirm.
const visible = (selector) => `(() => { const el = document.querySelector(${JSON.stringify(selector)}); return !!el && el.getClientRects().length > 0 })()`
const fills = `(() => { const r = document.querySelector('.crash').getBoundingClientRect(); return r.left === 0 && r.top === 0 && r.width === innerWidth && r.height === innerHeight })()`

test('the windows show a pane with null lists and on an error, and Leave asks first', { skip: !hasChrome() && 'Google Chrome is not here', timeout: 120000 }, async () => {
  const { createServer } = await import('vite')
  const server = await createServer({
    configFile: fileURLToPath(new URL('../dev/vite.config.js', import.meta.url)),
    logLevel: 'silent',
    server: { host: '127.0.0.1', port: 0, strictPort: false },
  })
  await server.listen()
  const base = `http://127.0.0.1:${server.httpServer.address().port}`
  const { send, evaluate, until, close } = await openChrome(['--lang=en-US'])
  const open = async (path, ready) => {
    await send('Page.navigate', { url: base + path })
    assert.ok(await until(ready), `${path}: ${ready} never showed`)
  }
  const press = (text) => evaluate(`[...document.querySelectorAll('button')].find((b) => b.textContent.trim().startsWith(${JSON.stringify(text)}) || b.getAttribute('aria-label') === ${JSON.stringify(text)}).click()`)
  const room = (name) => evaluate(`document.querySelector('.conv[title=${JSON.stringify(name)}]').click()`)
  const rect = (selector) => evaluate(`(() => { const r = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width } })()`)
  const click = async (x, y) => {
    await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y })
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 })
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 })
  }
  const CODES = { Enter: 13, Escape: 27, Tab: 9, ArrowDown: 40, ArrowUp: 38, Home: 36, End: 35 }
  const key = async (k) => {
    await send('Input.dispatchKeyEvent', { type: 'keyDown', key: k, code: k, windowsVirtualKeyCode: CODES[k], ...(k === 'Enter' ? { text: '\r' } : {}) })
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key: k, code: k, windowsVirtualKeyCode: CODES[k] })
  }
  const focused = `document.activeElement.textContent.trim()`
  const inBox = `document.querySelector('.modal__box').contains(document.activeElement)`
  const onAdd = `document.activeElement === document.querySelector('.add button')`
  const type = (selector, text) => evaluate(`(() => { const input = document.querySelector(${JSON.stringify(selector)}); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, ${JSON.stringify(text)}); input.dispatchEvent(new Event('input', { bubbles: true })) })()`)
  try {
    await send('Page.enable')
    await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false })

    // A room that cannot reach the network, one that reconnects and one that connects: each row has its dot, the header
    // and the notes say it in plain words, the composer stays, and no error shows.
    await open('/?scene=offline', visible('.composer'))
    assert.equal(await evaluate(`[...document.querySelectorAll('.conv__net')].map((d) => d.className.replace('conv__net ', '')).join()`), 'is-offline,is-reconnecting,is-connecting')
    assert.equal(await evaluate(`document.querySelector('.chathead__sub .is-pending').textContent`), 'Could not reach the network, trying again')
    assert.match(await evaluate(`[...document.querySelectorAll('.note')].map((n) => n.textContent).join(' ')`), /Messages you type are kept and sent when the connection returns/)
    assert.equal(await evaluate(`document.querySelector('.error') === null && document.querySelector('.fault') === null`), true, 'no error shows')
    await room('Work')
    assert.equal(await evaluate(`document.querySelector('.chathead__sub .is-pending').textContent`), 'Reconnecting...')
    await room('Book club')
    assert.equal(await evaluate(`document.querySelector('.chathead__sub .is-pending').textContent`), 'Connecting...')
    assert.equal(await evaluate(`document.querySelector('.composer__input') !== null`), true, 'the composer stays')

    // Every list as null: the room, then each page that reads a list, shows; no error pane.
    await open('/?scene=nulls', visible('.composer'))
    await sleep(300)
    assert.ok(await evaluate(visible('.convs')), 'the sidebar shows')
    for (const page of ['Settings', 'Invite', 'Profile']) {
      await press(page)
      assert.ok(await until(visible('.page')), `${page} shows`)
    }
    assert.equal(await evaluate(`document.querySelector('.crash') === null`), true, 'no error pane')

    // An error outside a render: the pane over the window, which Dismiss gives back.
    await evaluate(`setTimeout(() => { throw new Error('an event handler failed') })`)
    assert.ok(await until(visible('.crash')), 'the pane shows')
    assert.match(await evaluate(`document.querySelector('.crash__text').textContent`), /^Error: an event handler failed/)
    assert.ok(await evaluate(fills), 'the pane fills the window')
    await press('Dismiss')
    assert.ok(await until(`document.querySelector('.crash') === null`), 'Dismiss hides the pane')
    assert.ok(await evaluate(visible('.page')), 'the window goes on where it was')

    // A render that throws: the pane in place of the window, with the component stack, sent to Go, and Copy.
    await open('/?scene=crash', visible('.crash'))
    assert.equal(await evaluate(`document.querySelector('.crash').getAttribute('role')`), 'alert')
    assert.equal(await evaluate(`document.querySelector('.crash h1').textContent`), 'This window stopped on an error')
    assert.equal(await evaluate(`document.querySelector('.app') === null`), true, 'the pane is in place of the window')
    assert.equal(await evaluate(`[...document.querySelectorAll('.crash button')].some((b) => b.textContent === 'Dismiss')`), false, 'no Dismiss after a render error')
    assert.ok(await evaluate(fills), 'the pane fills the window')
    const text = await evaluate(`document.querySelector('.crash__text').textContent`)
    assert.match(text, /Objects are not valid as a React child/)
    assert.match(text, /at App/)
    assert.ok(await until(`globalThis.reports.some((r) => /Objects are not valid/.test(r.message) && /at App/.test(r.stack))`), 'the error reached ReportError')
    await press('Copy the error')
    assert.ok(await until(`globalThis.copied === document.querySelector('.crash__text').textContent`), 'Copy copies the text')
    assert.ok(await until(`document.querySelector('.crash__status').textContent === 'Copied'`))

    // Three rooms in one list: a message of another one counts as unread in the list until it is selected, and its
    // messages show only there. Nothing of an inner level shows: no list of rooms inside a room, no box for a new one.
    await open('/', `document.querySelectorAll('.conv').length === 3 && document.querySelectorAll('.bubble').length >= 11`)
    assert.equal(await evaluate(`document.querySelectorAll('.room, .sidebar, .sidebar__add, input[aria-label="New room"]').length`), 0, 'one list level, no inner rooms')
    assert.equal(await evaluate(`document.body.textContent.includes('# general')`), false, 'no # general anywhere')
    assert.equal(await evaluate(`document.querySelector('.convs__heading .sidebar__title').textContent`), 'Rooms')
    assert.deepEqual(await evaluate(`[...document.querySelectorAll('.chathead__actions .pill')].map((b) => b.textContent.trim())`), ['Members', 'Profile'], 'only the buttons the app has')
    assert.ok(await until(`document.querySelector('.conv[title="Work"] .conv__unread')?.textContent === '1 new'`), 'Work has 1 new, from the default room')
    await room('Work')
    assert.ok(await until(`document.querySelector('.conv.is-active').title === 'Work'`), 'selecting Work reads it')
    assert.ok(await until(`[...document.querySelectorAll('.bubble')].map((b) => b.textContent).join('|') === 'Stand-up moved to ten.'`), "only Work's default room shows")
    assert.equal(await evaluate(`document.querySelector('.chathead__name').textContent`), 'Work')
    assert.equal(await evaluate(`document.querySelector('.conv[title="Work"] .conv__unread') === null`), true, 'reading it clears the count')

    // The members panel: Maya is an admin and the settler in Work: Remove on the others, never on herself; the
    // confirmation says what removal does not do, and only its own button removes.
    assert.equal(await evaluate(`document.querySelector('.members') === null`), true, 'the panel starts closed')
    await press('Members')
    assert.ok(await until(visible('.members')), 'Members opens the panel')
    assert.equal(await evaluate(`document.querySelector('[aria-controls="members-panel"]').getAttribute('aria-expanded')`), 'true')
    const removes = `[...document.querySelectorAll('.member')].filter((m) => m.querySelector('.member__remove')).map((m) => m.querySelector('.member__name').textContent)`
    assert.ok(await until(`${removes}.join('|') === 'Leo|Sam'`), 'Remove on the others only')
    await evaluate(`[...document.querySelectorAll('.member')].find((m) => m.textContent.includes('Leo')).querySelector('.member__remove').click()`)
    assert.ok(await until(visible('.confirm')), 'the confirmation shows')
    assert.match(await evaluate(`document.querySelector('.confirm').textContent`), /Remove Leo from the member list\?.*keep the room key/)
    assert.equal(await evaluate(`globalThis.removed === undefined`), true, 'asking is not removing')
    await evaluate(`document.querySelector('.confirm .btn--danger').click()`)
    assert.ok(await until(`globalThis.removed === '7a3c5e19b2d4f60817a3c5e19b2d4f60817a3c5e19b2d4f60817a3c5e19b2d4f6'`), 'the confirm removes Leo')
    await room('Lake trip')
    assert.ok(await until(`document.querySelector('.conv.is-active').title === 'Lake trip'`))
    assert.ok(await until(`${removes}.length === 0 && document.querySelectorAll('.member').length === 4`), 'a member sees no Remove')
    assert.equal(await evaluate(`[...document.querySelectorAll('.pill')].some((b) => b.textContent.startsWith('Invite'))`), false, 'a member has no Invite')
    await press('Members')
    assert.ok(await until(`document.querySelector('.members') === null`), 'Members closes the panel')

    // Grouping: the author once above the first message of a group, a bubble per message, the time once under the group,
    // a day chip when the day changes, and a new group when the author changes or more than five minutes passed.
    const chips = await evaluate(`[...document.querySelectorAll('.day__chip')].map((c) => c.textContent)`)
    assert.deepEqual(chips, ['Yesterday', 'Today'])
    assert.deepEqual(await evaluate(`[...document.querySelectorAll('.group')].map((g) => g.querySelector('.group__from').textContent)`), ['Leo', 'Priya', 'You', 'Sam', 'Leo', 'Priya', 'You', 'You'])
    assert.deepEqual(await evaluate(`[...document.querySelectorAll('.group')].map((g) => g.querySelectorAll('.bubble').length)`), [2, 1, 2, 1, 2, 1, 1, 1])
    assert.equal(await evaluate(`[...document.querySelectorAll('.group')].every((g) => g.querySelectorAll('.group__from').length === 1 && g.querySelectorAll('.group__meta').length === 1)`), true, 'the name and the time show once per group')
    assert.equal(await evaluate(`document.querySelectorAll('.group__via').length`), 2, 'the source shows once per group that came from the pin')
    assert.equal(await evaluate(`document.querySelector('.group__via').textContent.includes('synced from pin')`), true)
    const mine = await evaluate(`(() => { const m = document.querySelector('.group.is-mine').getBoundingClientRect(); const t = document.querySelector('.group.is-theirs').getBoundingClientRect(); return { mineRight: m.right, theirsLeft: t.left, theirsRight: t.right, width: innerWidth } })()`)
    assert.ok(mine.mineRight > mine.width * 0.8 && mine.theirsLeft < mine.width * 0.5, 'mine on the right, the others on the left')
    assert.equal(await evaluate(`[...document.querySelectorAll('.bubble')].every((b) => b.scrollWidth <= b.clientWidth + 1) && document.documentElement.scrollWidth <= innerWidth`), true, 'a long invite string wraps and nothing scrolls sideways')
    assert.match(await evaluate(`document.querySelector('.composer__note').textContent`), /encrypted and go straight between the members' computers/)
    // Sending shows the message as mine, with the same rule.
    await type('.composer__input', 'On my way')
    await evaluate(`document.querySelector('.composer__form').requestSubmit()`)
    assert.ok(await until(`[...document.querySelectorAll('.bubble')].at(-1).textContent === 'On my way'`), 'my message shows')
    assert.equal(await evaluate(`[...document.querySelectorAll('.group')].at(-1).querySelector('.group__from').textContent`), 'You')

    // The sidebar collapses to a rail of avatars: state, aria, the width given to the main column, the tooltip names,
    // the choice kept across a reload, and the keyboard.
    const toggle = `document.querySelector('[aria-controls="rooms-panel"]')`
    assert.equal(await evaluate(`${toggle}.getAttribute('aria-expanded')`), 'true')
    assert.equal(await evaluate(`${toggle}.getAttribute('aria-label')`), 'Collapse the sidebar')
    const open0 = { sidebar: (await rect('.convs')).width, main: (await rect('.main')).width }
    await evaluate(`${toggle}.focus()`)
    await key('Enter')
    assert.ok(await until(`document.querySelector('.convs.is-collapsed') !== null`), 'Enter on the toggle collapses it')
    assert.equal(await evaluate(`${toggle}.getAttribute('aria-expanded')`), 'false')
    assert.equal(await evaluate(`${toggle}.getAttribute('aria-label')`), 'Expand the sidebar')
    await sleep(300)
    const shut = { sidebar: (await rect('.convs')).width, main: (await rect('.main')).width }
    assert.ok(shut.sidebar <= 80 && shut.main > open0.main + (open0.sidebar - 80) - 2, `the main column takes the freed width: ${JSON.stringify({ open0, shut })}`)
    assert.equal(await evaluate(`getComputedStyle(document.querySelector('.conv__text')).display`), 'none', 'only the avatars show')
    assert.deepEqual(await evaluate(`[...document.querySelectorAll('.conv')].map((b) => [b.getAttribute('aria-label'), b.title])`), [['Lake trip', 'Lake trip'], ['Work', 'Work'], ['Book club', 'Book club']], 'the name is the label and the tooltip')
    assert.equal(await evaluate(`localStorage.getItem('kolo-chat.sidebar')`), 'collapsed', 'the choice is stored')
    assert.equal(await evaluate(`${toggle}.matches(':focus-visible')`), true, 'the toggle keeps a visible focus')
    await open('/', `document.querySelectorAll('.conv').length === 3`)
    assert.ok(await evaluate(`document.querySelector('.convs.is-collapsed') !== null`), 'a reload keeps it collapsed')
    // Storage that throws: the window still works, expanded.
    await send('Page.addScriptToEvaluateOnNewDocument', { source: `Object.defineProperty(window, 'localStorage', { get() { throw new Error('blocked') } })` })
    await open('/', `document.querySelectorAll('.conv').length === 3`)
    assert.equal(await evaluate(`document.querySelector('.convs.is-collapsed') === null`), true, 'without storage it is expanded')
    await evaluate(`document.querySelector('[aria-controls="rooms-panel"]').click()`)
    assert.ok(await until(`document.querySelector('.convs.is-collapsed') !== null`), 'and it still collapses')
    await send('Page.navigate', { url: 'about:blank' })

    // The add menu under "+": two rows with an icon, a title and a quiet line; arrows, Home, End, Enter, Escape and a
    // click outside; the focus goes back to the button. A row opens a dialog with the form.
    await open('/', `document.querySelectorAll('.conv').length === 3 && document.querySelector('.convs.is-collapsed') === null`)
    const plus = `document.querySelector('.add button')`
    assert.equal(await evaluate(`${plus}.getAttribute('aria-haspopup')`), 'menu')
    assert.equal(await evaluate(`document.querySelector('.popover') === null`), true, 'closed at first')
    await press('Add a room')
    assert.ok(await until(visible('.popover')), 'the menu opens')
    assert.equal(await evaluate(`${plus}.getAttribute('aria-expanded')`), 'true')
    assert.deepEqual(await evaluate(`[...document.querySelectorAll('[role="menuitem"]')].map((i) => [i.querySelector('.choice__title').textContent, i.querySelector('.choice__sub').textContent.length > 0, i.querySelector('.choice__icon svg') !== null])`), [['Create a room', true, true], ['Join a room', true, true]])
    assert.match(await evaluate(focused), /^Create a room/, 'the first row has the focus')
    await key('ArrowDown')
    assert.match(await evaluate(focused), /^Join a room/)
    await key('ArrowDown')
    assert.match(await evaluate(focused), /^Create a room/, 'the arrows wrap')
    await key('ArrowUp')
    assert.match(await evaluate(focused), /^Join a room/)
    await key('Home')
    assert.match(await evaluate(focused), /^Create a room/)
    await key('End')
    assert.match(await evaluate(focused), /^Join a room/)
    await key('Escape')
    assert.ok(await until(`document.querySelector('.popover') === null`), 'Escape closes the menu')
    assert.equal(await evaluate(onAdd), true, 'the focus is back on the button')
    assert.equal(await evaluate(`${plus}.getAttribute('aria-expanded')`), 'false')
    await key('ArrowDown')
    assert.ok(await until(visible('.popover')), 'ArrowDown on the button opens it too')
    await click(900, 500)
    assert.ok(await until(`document.querySelector('.popover') === null`), 'a click outside closes the menu')
    assert.equal(await evaluate(`document.querySelector('.modal') === null`), true, 'and opens nothing')

    // Create a room: the dialog, with the focus inside and kept there; Escape, a click outside and the Cancel button
    // close it and give the focus back to "+".
    await press('Add a room')
    assert.ok(await until(visible('.popover')))
    await key('Enter')
    assert.ok(await until(visible('.modal')), 'Enter on a row opens the dialog')
    assert.equal(await evaluate(`document.querySelector('.popover') === null`), true, 'the menu is gone')
    assert.equal(await evaluate(`document.querySelector('.modal__box').getAttribute('role') + document.querySelector('.modal__box').getAttribute('aria-modal')`), 'dialogtrue')
    assert.equal(await evaluate(`document.getElementById(document.querySelector('.modal__box').getAttribute('aria-labelledby')).textContent`), 'Create a room')
    assert.equal(await evaluate(inBox), true, 'the focus starts inside')
    assert.equal(await evaluate(`document.activeElement.classList.contains('field')`), true, 'on the name field')
    for (let i = 0; i < 6; i++) {
      await key('Tab')
      assert.equal(await evaluate(inBox), true, `Tab ${i + 1} stays in the dialog`)
    }
    await key('Escape')
    assert.ok(await until(`document.querySelector('.modal') === null`), 'Escape closes the dialog')
    assert.equal(await evaluate(onAdd), true, 'the focus is back on "+"')
    await press('Add a room')
    await until(visible('.popover'))
    await key('Enter')
    assert.ok(await until(visible('.modal')))
    await click(5, 5)
    assert.ok(await until(`document.querySelector('.modal') === null`), 'a click outside closes the dialog')
    assert.equal(await evaluate(onAdd), true, 'the focus is back on "+" after the click')
    await press('Add a room')
    await until(visible('.popover'))
    await key('Enter')
    assert.ok(await until(visible('.modal')))
    const box = await rect('.modal__box')
    await click((box.left + box.right) / 2, box.top + 20)
    await sleep(200)
    assert.equal(await evaluate(`document.querySelector('.modal') !== null`), true, 'a click inside does not close it')
    assert.ok(Math.abs((box.left + box.right) / 2 - 720) < 2, 'the dialog is centered')
    await type('.modal .field', 'Garden')
    await press('Create')
    assert.ok(await until(`document.querySelector('.modal') === null && document.querySelectorAll('.conv').length === 4`), 'Create adds the room and closes the dialog')
    assert.equal(await evaluate(`document.querySelector('.conv.is-active').title`), 'Garden', 'and selects it')
    assert.equal(await evaluate(`document.querySelector('.chathead__name').textContent`), 'Garden')

    // Join a room: the second row opens the dialog with the invite code and a name, and Cancel closes it.
    await press('Add a room')
    await until(visible('.popover'))
    await key('ArrowDown')
    await key('Enter')
    assert.ok(await until(visible('.modal')))
    assert.equal(await evaluate(`document.querySelector('.modal__title').textContent`), 'Join a room')
    assert.equal(await evaluate(`document.querySelector('.modal [aria-label="Invite code"]') !== null && document.querySelectorAll('.modal .field').length === 2`), true)
    await key('Escape')
    assert.ok(await until(`document.querySelector('.modal') === null`))
    assert.equal(await evaluate(onAdd), true)

    // A window of about 800 px: the sidebar shrinks, the header keeps its buttons, nothing scrolls sideways.
    await send('Emulation.setDeviceMetricsOverride', { width: 800, height: 600, deviceScaleFactor: 1, mobile: false })
    await sleep(300)
    assert.equal(await evaluate(`document.documentElement.scrollWidth <= innerWidth`), true, 'no sideways scroll at 800')
    assert.ok((await rect('.convs')).width <= 260, 'the sidebar shrinks')
    assert.ok((await rect('.main')).width >= 540, 'the main column keeps the rest')
    assert.equal(await evaluate(`[...document.querySelectorAll('.chathead__actions .pill')].every((b) => b.getBoundingClientRect().right <= innerWidth)`), true)
    assert.ok(await evaluate(visible('.composer__input')))
    await send('Emulation.setDeviceMetricsOverride', { width: 2000, height: 1240, deviceScaleFactor: 1, mobile: false })
    await sleep(200)
    const wide = await rect('.main')
    assert.ok(wide.right === 2000 && (await rect('.convs')).left === 0, 'the window is filled at 2000')
    await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false })

    // The public network is a sentence with no address; your own network lists its entry nodes.
    await open('/', `document.querySelectorAll('.conv').length === 3`)
    await press('Settings')
    assert.ok(await until(visible('.network-note')))
    const settings = await evaluate(`document.querySelector('.page').textContent`)
    assert.match(settings, /public Kolo network/)
    assert.ok(!settings.includes('Entry nodes') && !settings.includes('7447'), 'no entry node of the public network shows')
    assert.match(settings, /About\s*Version dev/, 'the release shows, "dev" for a build that is not one')
    await open('/?scene=own-network', `document.querySelectorAll('.conv').length === 1`)
    await press('Settings')
    assert.ok(await until(`document.querySelector('.page').textContent.includes('127.0.0.1:7447')`), 'your own entry nodes show')

    // No pin: the add menu works, and one quiet line in the sidebar says that messages reach people only while they are
    // online. With pins, there is no line.
    await open('/', `document.querySelectorAll('.conv').length === 3`)
    assert.equal(await evaluate(`document.querySelector('.convs__note') === null`), true, 'with pins: no note')
    await open('/?scene=no-pin', visible('.composer'))
    assert.ok(await until(`document.querySelector('.convs__note')?.textContent === 'No pin: messages reach people only while they are online.'`), 'the sidebar says it')
    assert.ok(await evaluate(`document.querySelector('.convs__note').title.startsWith('This room has no pin')`), 'with the whole sentence in its tooltip')
    assert.equal(await evaluate(`[...document.querySelectorAll('.note')].some((n) => n.textContent.startsWith('This room has no pin'))`), false, 'not repeated above the composer')
    assert.equal(await evaluate(`document.querySelector('.add button').disabled`), false, 'the add button is enabled without a pin')
    await open('/?scene=join', visible('.join'))
    assert.equal(await evaluate(`document.querySelector('.add button').disabled`), false, 'the add button is enabled with nothing started')
    assert.equal(await evaluate(`document.querySelector('.chathead') === null`), true, 'no room header on the first-run screen')

    // Leave room: the plan with the room's folder first, Cancel keeps it, and only the confirm leaves. With the last
    // one gone the join screen shows. Each navigation loads a fresh mock.
    await open('/?scene=one', visible('.composer'))
    await press('Settings')
    await press('Leave room')
    assert.ok(await until(visible('.leave')), 'the confirm step shows')
    const plan = await evaluate(`document.querySelector('.leave').textContent`)
    assert.match(plan, /its folder, with its key, its messages, its invites and your kolo-id in it/)
    assert.match(plan, /still see you on the member list until an admin removes you, and can still read what you sent/)
    for (const path of ['conversations/a1b2c3d4e5f60718293a4b5c6d7e8f90', 'layout.json', 'notch.json']) {
      assert.ok(plan.includes(`kolo-chat/${path}`), `the plan names ${path}`)
    }
    assert.equal(await evaluate(`globalThis.left === true`), false, 'showing the plan does not leave')
    await press('Cancel')
    assert.ok(await until(`document.querySelector('.leave') === null`), 'Cancel closes the confirm step')
    assert.equal(await evaluate(`globalThis.left === true`), false, 'Cancel does not leave')
    await press('Leave room')
    assert.ok(await until(visible('.leave')))
    await press('Leave and delete')
    assert.ok(await until(`globalThis.left === true`), 'the confirm leaves')
    assert.ok(await until(visible('.join')), 'the join screen shows')
    assert.equal(await evaluate(`document.querySelector('.composer') === null && document.querySelector('.chathead') === null`), true, 'no room is left in the window')

    // The notch window: a rejected promise nobody caught shows the pane there too, with the notch's own size.
    await send('Emulation.setDeviceMetricsOverride', { width: 660, height: 92, deviceScaleFactor: 1, mobile: false })
    await open('/notch.html', visible('.notch'))
    await evaluate(`void Promise.reject(new Error('the notch failed'))`)
    assert.ok(await until(visible('.crash.is-compact')), 'the notch pane shows')
    assert.ok(await evaluate(fills), 'the notch pane fills the window')
    assert.ok(await until(`globalThis.reports.some((r) => r.message === 'Error: the notch failed')`), 'the notch error reached ReportError')
  } finally {
    await close()
    await server.close()
  }
})
