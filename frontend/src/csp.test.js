import test from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { extname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { hasChrome, openChrome } from '../dev/chrome.mjs'

const policy = `default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'`
const frontend = fileURLToPath(new URL('..', import.meta.url))

test('both pages carry the content security policy and no inline code', () => {
  for (const page of ['index.html', 'notch.html']) {
    const html = readFileSync(join(frontend, page), 'utf8')
    assert.ok(html.includes(`<meta http-equiv="Content-Security-Policy" content="${policy}" />`), page)
    assert.ok(!/<script(?![^>]*\bsrc=)|<style|\sstyle=|\son\w+=/.test(html), `${page} has inline code`)
  }
})

test('the built windows run under the policy: they render and nothing is blocked', { skip: !hasChrome() && 'Google Chrome is not here', timeout: 120000 }, async () => {
  const { build } = await import('vite')
  const out = mkdtempSync(join(tmpdir(), 'kolo-chat-csp-'))
  await build({
    configFile: join(frontend, 'dev', 'vite.config.js'),
    logLevel: 'silent',
    build: { outDir: out, emptyOutDir: true, rollupOptions: { input: [join(frontend, 'index.html'), join(frontend, 'notch.html')] } },
  })
  const types = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml' }
  const server = createServer((req, res) => {
    try {
      const body = readFileSync(join(out, new URL(req.url, 'http://x').pathname.replace(/\/+$/, '/index.html').replace(/\.\./g, '')))
      res.writeHead(200, { 'content-type': types[extname(req.url.split('?')[0])] || 'text/html' }).end(body)
    } catch {
      res.writeHead(404).end()
    }
  })
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve))
  const base = `http://127.0.0.1:${server.address().port}`
  const { send, evaluate, until, close } = await openChrome(['--lang=en-US'])
  try {
    await send('Page.enable')
    for (const [path, ready] of [['/', `document.querySelectorAll('.conv').length === 3`], ['/notch.html', `document.querySelector('.notch') !== null`]]) {
      await send('Page.addScriptToEvaluateOnNewDocument', { source: `globalThis.violations = []; document.addEventListener('securitypolicyviolation', (e) => globalThis.violations.push(e.violatedDirective))` })
      await send('Page.navigate', { url: base + path })
      assert.ok(await until(ready), `${path} never rendered`)
      assert.ok(await evaluate(`document.querySelector('meta[http-equiv="Content-Security-Policy"]') !== null`), `${path} lost its policy`)
      assert.deepEqual(await evaluate('globalThis.violations'), [], `${path} was blocked by the policy`)
      // The probe: an inline style attribute is refused, so a violation would have been seen.
      await evaluate(`document.body.setAttribute('style', 'color: red')`)
      assert.ok(await until(`globalThis.violations.length > 0`), `${path} does not enforce the policy`)
    }
  } finally {
    await close()
    server.close()
    rmSync(out, { recursive: true, force: true })
  }
})
