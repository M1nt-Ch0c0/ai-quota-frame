// Run from web/: node tools/check-ui.mjs
// All HTTP data are loopback fixtures. A fresh headless Chrome profile is used;
// this never attaches to a user's browser, real CPM, or Bluetooth device.
import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { spawn, execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { mkdtemp, mkdir, readFile, writeFile, readdir, rm } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromePath } from './chrome.mjs';

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const repo = path.dirname(web);
const output = process.env.PHOTOFRAME_UI_OUTPUT || path.join(tmpdir(), 'photopainter-v2-ui');
const screenshots = process.env.PHOTOFRAME_UI_CAPTURE !== '0';
const chrome = chromePath();
await mkdir(output, { recursive: true });
const profile = await mkdtemp(path.join(tmpdir(), 'photopainter-ui-profile-'));
const downloads = await mkdtemp(path.join(tmpdir(), 'photopainter-ui-download-'));
execFileSync('npm', ['run', 'build'], { cwd: web, stdio: 'inherit' });
const index = await readFile(path.join(web, 'index.html'), 'utf8');
const adapter = path.join(web, 'tools/ui-fixture-adapter.mjs');
const result = await build({ absWorkingDir: web, entryPoints: ['src/main.ts'], bundle: true, format: 'iife', write: false,
  plugins: [{ name: 'explicit-local-ui-fixtures', setup(builder) {
    builder.onResolve({ filter: /(?:ble\/(?:gatt-link|pairing)|photoframe\/sender)\.ts$/ }, () => ({ path: adapter }));
  } }],
});
const panel = index.replace('<!-- SCRIPT -->', () => `<script>${result.outputFiles[0].text.replaceAll('</script', '<\\/script')}</script>`);
const fixtureJSON = async name => JSON.parse(await readFile(path.join(repo, 'internal/cliproxy/testdata', name), 'utf8'));
const [codex, claude, xai] = await Promise.all(['codex_usage.json', 'claude_usage.json', 'xai_billing_fresh_week.json'].map(fixtureJSON));
const state = { failure: false, usageFailure: false, listFailure: false, delayMs: 0, variant: 0, lists: 0, api: 0, usage: 0, subjects: 0, forbidden: [], requests: [] };
const findings = [], checks = [], captures = [], layouts = [], errors = [], external = [];
let origin, browser, browserCDP, page;
const server = createServer(async (request, response) => {
  const url = new URL(request.url, origin);
  const send = (code, body, type = 'application/json') => { response.writeHead(code, { 'Content-Type': type }); response.end(typeof body === 'string' || body instanceof Uint8Array ? body : JSON.stringify(body)); };
  try {
    if (url.pathname === '/panel.html') return send(200, panel, 'text/html');
    if (url.pathname === '/production.html') return send(200, await readFile(path.join(web, 'dist/panel.html')), 'text/html');
    if (url.pathname === '/favicon.ico') return send(204, '');
    if (['/wasm_exec.js', '/frame.wasm'].includes(url.pathname)) return send(200, await readFile(path.join(web, 'dist', url.pathname.slice(1))), url.pathname.endsWith('.wasm') ? 'application/wasm' : 'text/javascript');
    state.requests.push(url.pathname);
    if (state.delayMs) await new Promise(resolve => setTimeout(resolve, state.delayMs));
    if (request.headers.authorization !== 'Bearer fixture-management-key') return send(401, { error: 'fixture management key required' });
    if (url.pathname === '/v0/management/auth-files') {
      state.lists++;
      if (state.listFailure) return send(503, { error: 'fixture list outage' });
      return send(200, { files: [
        { name: 'codex.json', provider: 'codex', account_type: 'oauth', auth_index: 'codex-fixture', email: 'codex@example.test', plan_type: 'plus', id_token: { chatgpt_account_id: 'workspace-fixture' } },
        { name: 'claude.json', provider: 'claude', account_type: 'oauth', auth_index: 'claude-fixture', email: 'claude@example.test', plan_type: 'max' },
        { name: 'xai.json', provider: 'xai', account_type: 'oauth', auth_index: 'xai-fixture', email: 'grok@example.test', plan_type: 'heavy' },
        { name: 'kimi.json', provider: 'kimi', account_type: 'oauth', auth_index: 'kimi-fixture', disabled: true },
      ] });
    }
    if (url.pathname === '/v0/management/plugins/photopainter/account-subject') {
      state.subjects++; assert.equal(url.searchParams.get('name'), 'xai.json'); return send(200, { sub: 'xai-subject-fixture' });
    }
    if (url.pathname === '/v0/management/dashboard/summary') {
      state.usage++;
      if (state.usageFailure) return send(503, { error: 'fixture usage outage' });
      return send(200, { today: { total_tokens: 1234567 + state.variant, total_cost: 1.25 } });
    }
    if (url.pathname === '/v0/management/api-call') {
      state.api++; assert.equal(request.method, 'POST'); assert.equal(request.headers.origin, origin);
      const chunks = []; for await (const chunk of request) chunks.push(chunk);
      const body = JSON.parse(Buffer.concat(chunks));
      let data, status = 200;
      if (body.url.includes('/wham/usage')) data = codex;
      else if (body.url.endsWith('/wham/rate-limit-reset-credits')) data = { credits: [] };
      else if (body.url.includes('anthropic.com') && body.url.endsWith('/usage')) { data = structuredClone(claude); data.five_hour.utilization += state.variant; }
      else if (body.url.includes('cli-chat-proxy.grok.com') && body.url.includes('billing')) { assert.equal(body.header['x-userid'], 'xai-subject-fixture'); data = xai; }
      else { state.forbidden.push(body.url); status = 404; data = {}; }
      if (state.failure) { status = 503; data = { error: 'fixture provider outage' }; }
      return send(200, { status_code: status, body: JSON.stringify(data) });
    }
    state.forbidden.push(url.pathname); return send(404, { error: 'unrecognized fixture endpoint' });
  } catch (error) { errors.push(`Fixture server: ${error.message}`); return send(500, { error: 'fixture assertion failed' }); }
});

class CDP {
  constructor(socket) {
    this.socket = socket; this.counter = 0; this.pending = new Map(); this.events = [];
    socket.addEventListener('message', event => {
      const message = JSON.parse(event.data);
      if (message.id) {
        const pending = this.pending.get(message.id); if (!pending) return;
        clearTimeout(pending.timeout); this.pending.delete(message.id);
        if (message.error) pending.reject(new Error(message.error.message)); else pending.resolve(message.result);
      } else { this.events.push(message); this.onEvent?.(message); }
    });
  }
  static async connect(url) { const socket = new WebSocket(url); await once(socket, 'open'); return new CDP(socket); }
  call(method, params = {}) {
    const id = ++this.counter;
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => { this.pending.delete(id); reject(new Error(`CDP timeout: ${method}`)); }, 45000);
      this.pending.set(id, { resolve, reject, timeout }); this.socket.send(JSON.stringify({ id, method, params }));
    });
  }
  async evaluate(expression) {
    const result = await this.call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
    return result.result.value;
  }
  close() { this.socket.close(); }
}
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function wait(expression, label, timeout = 30000) {
  const end = Date.now() + timeout;
  do { if (await page.evaluate(expression)) return; await pause(50); } while (Date.now() < end);
  throw new Error(`Timed out: ${label}; UI=${JSON.stringify(await page.evaluate(`({status:document.querySelector('#status-text')?.textContent,error:document.querySelector('#source-error')?.textContent})`))}`);
}
async function click(selector) {
  const point = await page.evaluate(`(() => {const e=document.querySelector(${JSON.stringify(selector)});if(!e||e.disabled||!e.getClientRects().length)throw Error('Unavailable control: '+${JSON.stringify(selector)});e.scrollIntoView({block:'center'});const r=e.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`);
  await page.call('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...point });
  await page.call('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...point });
}
async function tap(selector) {
  const point = await page.evaluate(`(() => {const e=document.querySelector(${JSON.stringify(selector)});e.scrollIntoView({block:'center'});const r=e.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`);
  await page.call('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ ...point, id: 1 }] });
  await page.call('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
}
async function key(name) {
  await page.call('Input.dispatchKeyEvent', { type: 'keyDown', key: name, code: name, windowsVirtualKeyCode: name === 'Escape' ? 27 : 9 });
  await page.call('Input.dispatchKeyEvent', { type: 'keyUp', key: name, code: name, windowsVirtualKeyCode: name === 'Escape' ? 27 : 9 });
}
function check(name, condition, detail = '') {
  checks.push({ name, passed: !!condition, detail });
  if (!condition) findings.push({ severity: 'P1', name, detail });
  console.log(`${condition ? 'PASS' : 'FAIL'} ${name}${detail ? ': ' + detail : ''}`);
}
async function viewport(width, height, mobile = false) {
  await page.call('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile });
  await page.call('Emulation.setTouchEmulationEnabled', { enabled: mobile, maxTouchPoints: 1 });
  await pause(200);
}
async function capture(name) {
  for (const [width, height, mobile] of [[1440, 1000, false], [390, 844, true]]) {
    await viewport(width, height, mobile);
    await page.evaluate('window.scrollTo(0,0)');
    const layout = await page.evaluate(`(() => {const dialog=document.querySelector('dialog[open]'); const scope=dialog||document.querySelector('.pp-app');return {
      width:innerWidth,scrollWidth:document.documentElement.scrollWidth,height:innerHeight,scrollHeight:document.documentElement.scrollHeight,
      dialog:dialog?{id:dialog.id,width:dialog.getBoundingClientRect().width,height:dialog.getBoundingClientRect().height,scrollWidth:dialog.scrollWidth,clientWidth:dialog.clientWidth}:null,
      smallTargets:Array.from(scope.querySelectorAll('button,summary,.pp-check,select,input:not([type=checkbox]):not([type=file])')).filter(e=>e.getClientRects().length&&!e.hidden).map(e=>({id:e.id||e.textContent.trim(),rect:e.getBoundingClientRect()})).filter(e=>e.rect.width<44||e.rect.height<44).map(e=>({id:e.id,width:e.rect.width,height:e.rect.height})),
      unlabeled:Array.from(scope.querySelectorAll('button,input,select,textarea')).filter(e=>e.getClientRects().length&&!e.hidden&&e.type!=='file').filter(e=>!e.textContent.trim()&&!e.getAttribute('aria-label')&&!e.labels?.length).map(e=>e.id)
    }})()`);
    layouts.push({ name, mobile, ...layout });
    check(`${name} ${width}px has no horizontal overflow`, layout.scrollWidth <= width && (!layout.dialog || layout.dialog.scrollWidth <= layout.dialog.clientWidth), JSON.stringify(layout.dialog || { scrollWidth: layout.scrollWidth }));
    check(`${name} ${width}px controls have accessible labels`, layout.unlabeled.length === 0, layout.unlabeled.join(', '));
    if (mobile) check(`${name} mobile controls reach 44px`, layout.smallTargets.length === 0, JSON.stringify(layout.smallTargets));
    const file = path.join(output, `${name}-${width}.png`);
    if (screenshots) {
      const screenshot = await page.call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true, clip: { x: 0, y: 0, width, height: Math.max(height, layout.scrollHeight), scale: 1 } });
      await writeFile(file, Buffer.from(screenshot.data, 'base64'));
    }
    if (existsSync(file)) captures.push(file);
  }
  await viewport(1440, 1000);
}
async function previewDigest() { return page.evaluate(`(async()=>{const bytes=await(await fetch(document.querySelector('#preview').src)).arrayBuffer();return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)),b=>b.toString(16).padStart(2,'0')).join('')})()`); }
async function refresh() { await click('#read-cpm'); await wait(`!document.querySelector('#read-cpm').disabled && document.querySelector('#frame').getAttribute('aria-busy')==='false'`, 'source refresh'); }
async function sourceKey(value) {
  await click('#panel-key');
  await page.evaluate(`document.querySelector('#panel-key').value='';document.querySelector('#panel-key').dispatchEvent(new Event('input',{bubbles:true}))`);
  await page.call('Input.insertText', { text: value });
}

try {
  server.listen(0, '127.0.0.1'); await once(server, 'listening'); origin = `http://127.0.0.1:${server.address().port}`;
  browser = spawn(chrome, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--disable-sync', '--disable-extensions', '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--proxy-server=direct://', '--proxy-bypass-list=*', '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1', 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] });
  const ws = await new Promise((resolve, reject) => {
    let stderr = ''; const timeout = setTimeout(() => reject(new Error('Headless Chrome did not start')), 15000);
    browser.stderr.on('data', data => { stderr += data; const match = stderr.match(/DevTools listening on (ws:\/\/\S+)/); if (match) { clearTimeout(timeout); resolve(match[1]); } });
    browser.once('exit', code => { clearTimeout(timeout); reject(new Error(`Headless Chrome exited: ${code}`)); });
  });
  browserCDP = await CDP.connect(ws);
  await browserCDP.call('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: downloads, eventsEnabled: true });
  const remote = new URL(ws);
  const target = await (await fetch(`http://${remote.host}/json/new?about:blank`, { method: 'PUT' })).json();
  page = await CDP.connect(target.webSocketDebuggerUrl);
  page.onEvent = message => {
    if (message.method === 'Runtime.exceptionThrown') errors.push(message.params.exceptionDetails.exception?.description || message.params.exceptionDetails.text);
    if (message.method === 'Network.requestWillBeSent') { const url = message.params.request.url; if (/^https?:/.test(url) && !url.startsWith(origin + '/')) external.push(url); }
  };
  await Promise.all([page.call('Runtime.enable'), page.call('Page.enable'), page.call('Network.enable')]);
  await viewport(1440, 1000);
  await page.call('Page.navigate', { url: `${origin}/panel.html` });
  await wait(`!!globalThis.__photoPainterUIFixture && !document.querySelector('#connect').disabled`, 'fixture UI initialization');
  check('No manual device token field', await page.evaluate(`Array.from(document.querySelectorAll('input')).every(e=>!/token|device-key|push-key/i.test(e.id+' '+e.name))`));
  check('No permanent protocol disclaimer', await page.evaluate(`!/GATT|CoC|ABI|设备令牌|推图令牌/.test(document.body.innerText)`));
  check('Initial send/download controls disabled', await page.evaluate(`document.querySelector('#send').disabled&&document.querySelector('#download').disabled`));
  await viewport(390, 844, true); await tap('#read-cpm');
  check('Synthesized mobile tap opens source dialog', await page.evaluate(`document.querySelector('#source-dialog').open`));
  check('Source dialog focuses management key', await page.evaluate(`document.querySelector('#source-dialog').open&&document.activeElement.id==='panel-key'`));
  const focusTrace = [];
  for (let i = 0; i < 8; i++) {
    await key('Tab');
    focusTrace.push(await page.evaluate(`({tag:document.activeElement.tagName,id:document.activeElement.id,inside:!!document.activeElement.closest('#source-dialog'),pageFocused:document.hasFocus()})`));
  }
  // Native <dialog> can report BODY at Chrome's focus-cycle boundary. BODY
  // is not an interactive background control. Verify both sequential focus
  // and an explicit attempt to focus an underlying application button.
  const backgroundInert = await page.evaluate(`document.querySelector('#read-cpm').focus();document.activeElement.id!=='read-cpm'`);
  check('Native source dialog keeps background controls inert', focusTrace.every(step => step.inside || step.tag === 'BODY') && backgroundInert, JSON.stringify(focusTrace));
  await viewport(1440, 1000);
  await click('#source-save');
  check('Empty source key has actionable error', await page.evaluate(`!document.querySelector('#source-error').hidden&&document.querySelector('#source-error').textContent.includes('管理密钥')`));
  state.delayMs = 150;
  await sourceKey('wrong-fixture-management-key'); await click('#source-save');
  check('Pending source request reports loading', await page.evaluate(`document.querySelector('#source-state').textContent==='更新中'&&document.querySelector('#source-settings').dataset.state==='loading'&&document.querySelector('#send').disabled`));
  await wait(`!document.querySelector('#read-cpm').disabled`, 'invalid management key rejection');
  check('First invalid management key cannot create a usable preview', await page.evaluate(`document.querySelector('#source-state').textContent==='需检查'&&document.querySelector('#source-settings').dataset.state==='error'&&document.querySelector('#preview').hidden&&document.querySelector('#send').disabled&&document.querySelector('#download').disabled&&document.querySelector('#source-dialog').open&&!document.querySelector('#source-error').hidden`));
  await click('#clear-panel-key');
  check('Clearing failed source returns to missing state', await page.evaluate(`document.querySelector('#source-state').textContent==='未设置'&&document.querySelector('#source-settings').dataset.state==='missing'`));
  state.failure = true; state.usageFailure = true;
  await sourceKey('fixture-management-key'); await click('#source-save');
  await wait(`!document.querySelector('#read-cpm').disabled`, 'first total source outage');
  check('First total source outage cannot create sendable empty data', await page.evaluate(`document.querySelector('#source-state').textContent==='需检查'&&document.querySelector('#preview').hidden&&document.querySelector('#send').disabled&&document.querySelector('#download').disabled&&document.querySelector('#source-dialog').open&&!document.querySelector('#source-error').hidden`));
  await click('#clear-panel-key');
  state.failure = false; state.usageFailure = false; state.delayMs = 0;
  const beforeInitialSuccess = { api: state.api, subjects: state.subjects, usage: state.usage, lists: state.lists };
  await sourceKey('fixture-management-key'); await click('#source-save');
  await wait(`!document.querySelector('#preview').hidden&&!document.querySelector('#download').disabled&&!document.querySelector('#source-dialog').open`, 'real WASM preview', 45000);
  const initialRequests = Object.fromEntries(Object.keys(beforeInitialSuccess).map(key => [key, state[key] - beforeInitialSuccess[key]]));
  check('Real collector queried quota, reset cards, sanitized subject, and 7 usage days', initialRequests.api === 4 && initialRequests.subjects === 1 && initialRequests.usage === 7 && initialRequests.lists === 1, JSON.stringify(initialRequests));
  check('Successful source reports ready', await page.evaluate(`document.querySelector('#source-state').textContent==='已连接'&&document.querySelector('#source-settings').dataset.state==='ready'`));
  check('Management key cleared from visible form and not persisted', await page.evaluate(`document.querySelector('#panel-key').value===''&&!JSON.stringify(localStorage).includes('fixture-management-key')`));
  const originalDigest = await previewDigest();
  await capture('preview-mint-skin');
  await click('#download');
  const downloadDeadline = Date.now() + 15000; let png;
  while (Date.now() < downloadDeadline) { png = (await readdir(downloads)).find(name => name.endsWith('.png')); if (png) break; await pause(100); }
  assert(png, 'Downloaded PNG not found');
  const bytes = await readFile(path.join(downloads, png));
  check('Download matches displayed PNG bytes', createHash('sha256').update(bytes).digest('hex') === originalDigest, `${bytes.length} bytes`);
  await writeFile(path.join(output, 'downloaded-preview.png'), bytes);
  execFileSync('go', ['run', './web/tools/validate-png.go', path.join(output, 'downloaded-preview.png')], { cwd: repo, stdio: 'inherit' });
  await click('#view-settings');
  check('Settings dialog focuses theme', await page.evaluate(`document.activeElement.id==='theme'`));
  await capture('settings');
  await page.evaluate(`document.querySelector('#theme').value='classic'`); await click('#settings-save');
  await wait(`document.querySelector('#frame').getAttribute('aria-busy')==='false'&&!document.querySelector('#download').disabled`, 'theme render');
  check('Theme choice renders different bytes and persists', await previewDigest() !== originalDigest && await page.evaluate(`JSON.parse(localStorage.getItem('photopainter:display-v2')).theme==='classic'&&document.querySelector('#image-meta').textContent.includes('经典')`));
  await click('#view-settings');
  await page.evaluate(`for(const e of document.querySelectorAll('#providers input'))e.checked=false;document.querySelector('#providers').dispatchEvent(new Event('change',{bubbles:true}))`);
  check('Settings rejects zero selected providers', await page.evaluate(`document.querySelector('#settings-save').disabled`));
  await key('Escape'); await click('#view-settings');
  check('Reopened settings with restored providers is submittable', await page.evaluate(`!!document.querySelector('#providers input:checked')&&!document.querySelector('#settings-save').disabled`), 'Clear all providers, Escape, reopen settings');
  const priorPreferences = await page.evaluate(`localStorage.getItem('photopainter:display-v2')`);
  await page.evaluate(`for(const e of document.querySelectorAll('#providers input'))e.checked=true;document.querySelector('#providers').dispatchEvent(new Event('change',{bubbles:true}))`);
  check('Settings rejects more than five providers', await page.evaluate(`document.querySelectorAll('#providers input:checked').length===6&&document.querySelector('#settings-save').disabled&&document.querySelector('#providers').hasAttribute('data-invalid')`));
  await page.evaluate(`document.querySelector('#settings-dialog form').dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}))`);
  check('Direct settings submission cannot bypass the provider limit', await page.evaluate(`document.querySelector('#settings-dialog').open`) && await page.evaluate(`localStorage.getItem('photopainter:display-v2')`) === priorPreferences);
  await page.evaluate(`const all=document.querySelectorAll('#providers input');all[all.length-1].checked=false;document.querySelector('#providers').dispatchEvent(new Event('change',{bubbles:true}))`);
  check('Settings permits the native maximum of five providers', await page.evaluate(`document.querySelectorAll('#providers input:checked').length===5&&!document.querySelector('#settings-save').disabled`));
  await key('Escape'); await click('#view-settings');
  // A change event resumes the flow even when the preceding regression is found.
  await page.evaluate(`document.querySelector('#providers').dispatchEvent(new Event('change',{bubbles:true}))`);
  await key('Escape');
  await click('#connect'); await wait(`document.querySelector('#pair-dialog').dataset.state==='waiting'`, 'simulated six-digit confirmation');
  check('Pairing shows six digits and physical button instruction', await page.evaluate(`document.querySelector('.pp-code-digits').textContent==='482 619'&&document.querySelector('#pair-instruction').textContent.includes('按一下相框的功能键')&&document.activeElement.id==='pair-cancel'`));
  await capture('pairing-code-simulated');
  await page.evaluate(`globalThis.__photoPainterUIFixture.expirePair()`); await wait(`document.querySelector('#pair-dialog').dataset.state==='error'&&!document.querySelector('#connect').disabled`, 'simulated pairing error');
  check('Pairing error focuses retry', await page.evaluate(`document.activeElement.id==='pair-retry'&&!document.querySelector('#pair-error').hidden`));
  await capture('pairing-error-simulated');
  await click('#pair-retry'); await wait(`document.querySelector('#pair-dialog').dataset.state==='waiting'`, 'simulated retry');
  await page.evaluate(`globalThis.__photoPainterUIFixture.approvePair()`); await wait(`document.querySelector('#device').dataset.state==='connected'`, 'simulated pairing saved');
  check('Pairing success focuses complete and remembers device', await page.evaluate(`document.activeElement.id==='pair-complete'&&!document.querySelector('#device-info').hidden&&document.querySelector('#device-id').textContent==='010203040506'`));
  await capture('pairing-success-simulated');
  await click('#pair-complete');
  await click('#disconnect'); await click('#add-device');
  await wait(`document.querySelector('#device').dataset.state==='connected'&&document.querySelector('#pair-dialog').dataset.state==='success'`, 'existing binding selected again');
  check('Selecting saved device in picker completes its open modal', await page.evaluate(`document.querySelector('#pair-dialog').open&&document.querySelector('#pair-title').textContent==='相框已连接'&&document.activeElement.id==='pair-complete'`));
  await click('#pair-complete');
  const pickerCallsAfterPairing = await page.evaluate(`globalThis.__photoPainterUIFixture.pickerCalls`);
  // Seed the collector cache with the current theme/options before testing a
  // stale result. Both the UI button and the transfer entry guard must reject it.
  await refresh(); state.failure = true; await refresh();
  check('Cached stale data remains viewable but cannot be manually sent', await page.evaluate(`document.querySelector('#device').dataset.state==='connected'&&document.querySelector('#source-state').textContent==='需检查'&&document.querySelector('#source-settings').dataset.state==='error'&&document.querySelector('#status-text').textContent==='显示上次已知数据'&&!document.querySelector('#preview').hidden&&!document.querySelector('#download').disabled&&document.querySelector('#send').disabled`));
  await page.evaluate(`document.querySelector('#send').onclick(new MouseEvent('click'))`); await pause(100);
  check('Transfer entry rejects stale data even if its handler is invoked directly', await page.evaluate(`globalThis.__photoPainterUIFixture.sends.length===0&&!globalThis.__photoPainterUIFixture.pendingSend`));
  state.listFailure = true; state.delayMs = 150;
  await click('#read-cpm');
  check('Source refresh blocks manual send while loading', await page.evaluate(`document.querySelector('#source-state').textContent==='更新中'&&document.querySelector('#send').disabled`));
  await wait(`!document.querySelector('#read-cpm').disabled`, 'cached source request failure');
  check('Failed source request marks error and invalidates manual-send data', await page.evaluate(`document.querySelector('#device').dataset.state==='connected'&&document.querySelector('#source-state').textContent==='需检查'&&document.querySelector('#send').disabled&&document.querySelector('#download').disabled&&document.querySelector('#status-text').textContent==='更新未完成'`));
  await page.evaluate(`document.querySelector('#send').onclick(new MouseEvent('click'))`); await pause(100);
  check('Transfer entry rejects data after source request failure', await page.evaluate(`globalThis.__photoPainterUIFixture.sends.length===0&&!globalThis.__photoPainterUIFixture.pendingSend`));
  state.listFailure = false; state.failure = false; state.delayMs = 0; await refresh();
  check('Fresh recovery restores ready state and manual send', await page.evaluate(`document.querySelector('#source-state').textContent==='已连接'&&!document.querySelector('#send').disabled`));
  const sendDigest = await previewDigest(); await click('#send'); await wait(`!!globalThis.__photoPainterUIFixture.pendingSend`, 'simulated send');
  check('Sender receives the displayed PNG bytes', await page.evaluate(`globalThis.__photoPainterUIFixture.sends[0].digest`) === sendDigest);
  state.variant = 10; await refresh();
  check('Refreshing during send preserves frozen preview bytes', await previewDigest() === sendDigest);
  state.failure = true; await refresh();
  await page.evaluate(`globalThis.__photoPainterUIFixture.finishSend()`); await wait(`!globalThis.__photoPainterUIFixture.pendingSend&&!document.querySelector('#connect').disabled`, 'completion after stale refresh');
  check('Stale refresh discards queued image', await page.evaluate(`document.querySelector('#download').disabled&&document.querySelector('#send').disabled&&globalThis.__photoPainterUIFixture.sends.length===1`));
  state.failure = false; await refresh();
  await click('#connect'); await wait(`document.querySelector('#device').dataset.state==='connected'`, 'saved simulated reconnect');
  check('Saved binding reconnect avoids picker and code dialog', await page.evaluate(`globalThis.__photoPainterUIFixture.pickerCalls===${pickerCallsAfterPairing}&&!document.querySelector('#pair-dialog').open`));
  await click('#send'); await wait(`!!globalThis.__photoPainterUIFixture.pendingSend`, 'second simulated send');
  state.variant = 20; await refresh(); state.listFailure = true; await refresh();
  await page.evaluate(`globalThis.__photoPainterUIFixture.finishSend()`); await wait(`!globalThis.__photoPainterUIFixture.pendingSend&&!document.querySelector('#connect').disabled`, 'completion after failed refresh');
  check('Failed refresh discards queued image', await page.evaluate(`document.querySelector('#download').disabled&&document.querySelector('#send').disabled&&globalThis.__photoPainterUIFixture.sends.length===2`));
  state.listFailure = false; await refresh();
  await page.call('Page.reload'); await wait(`!document.querySelector('#connect').disabled&&document.querySelector('#connect').textContent==='连接相框'`, 'saved binding across reload');
  check('Binding persists while management credential does not', await page.evaluate(`document.querySelector('#device-name').textContent.includes('UI fixture')&&document.querySelector('#source-state').textContent==='未设置'&&document.querySelector('#panel-key').value===''`));
  await click('#connect'); await wait(`document.querySelector('#device').dataset.state==='connected'`, 'reconnect after reload');
  check('Reloaded binding reconnect uses granted device', await page.evaluate(`globalThis.__photoPainterUIFixture.pickerCalls===0&&!document.querySelector('#pair-dialog').open`));
  await click('#view-settings');
  await page.evaluate(`document.querySelector('#automatic').checked=true;document.querySelector('#refresh-seconds').value='60'`);
  await click('#settings-save');
  await click('#read-cpm'); await click('#panel-key'); await page.call('Input.insertText', { text: 'fixture-management-key' }); await click('#source-save');
  await wait(`!!globalThis.__photoPainterUIFixture.pendingSend`, 'automatic send after successful collection', 45000);
  check('Automatic mode sends a new successful image', await page.evaluate(`globalThis.__photoPainterUIFixture.sends.length===1`));
  await page.evaluate(`globalThis.__photoPainterUIFixture.finishSend()`); await wait(`!globalThis.__photoPainterUIFixture.pendingSend&&!document.querySelector('#connect').disabled`, 'automatic send completion');
  await refresh(); await pause(200);
  check('Unchanged collection is deduplicated', await page.evaluate(`globalThis.__photoPainterUIFixture.sends.length===1&&!globalThis.__photoPainterUIFixture.pendingSend`));
  state.failure = true; await refresh(); await pause(200);
  check('Automatic mode never sends stale source data', await page.evaluate(`globalThis.__photoPainterUIFixture.sends.length===1&&!globalThis.__photoPainterUIFixture.pendingSend`));
  state.failure = false; await refresh();
  state.variant = 30; await refresh(); await wait(`!!globalThis.__photoPainterUIFixture.pendingSend`, 'changed source automatic send');
  await click('#cancel'); await wait(`!globalThis.__photoPainterUIFixture.pendingSend&&!document.querySelector('#connect').disabled`, 'explicit auto-send cancellation');
  await refresh();
  check('Explicit cancel suppresses automatic resend of same image', await page.evaluate(`globalThis.__photoPainterUIFixture.sends.length===2&&!globalThis.__photoPainterUIFixture.pendingSend`));
  const listsBeforeCadence = state.lists;
  await page.call('Emulation.setVirtualTimePolicy', { policy: 'pauseIfNetworkFetchesPending', budget: 60100 });
  await wait(`!document.querySelector('#read-cpm').disabled&&document.querySelector('#frame').getAttribute('aria-busy')==='false'`, 'scheduled fixture refresh');
  const cadenceDeadline = Date.now() + 10000;
  while (Date.now() < cadenceDeadline && state.lists <= listsBeforeCadence) await pause(50);
  await wait(`!document.querySelector('#read-cpm').disabled&&document.querySelector('#frame').getAttribute('aria-busy')==='false'`, 'scheduled collection settled');
  check('60-second cadence refreshes source without resending cancelled image', state.lists === listsBeforeCadence + 1 && await page.evaluate(`globalThis.__photoPainterUIFixture.sends.length===2&&!globalThis.__photoPainterUIFixture.pendingSend`), `Source collections ${listsBeforeCadence} → ${state.lists}`);
  check('All requests remained local and allowed', external.length === 0 && state.forbidden.length === 0, JSON.stringify({ external, forbidden: state.forbidden }));
  check('No uncaught runtime/fixture errors', errors.length === 0, errors.join('\n'));
} catch (error) {
  findings.push({ severity: 'P0', name: 'UI test execution failed', detail: error.stack }); console.error(error);
} finally {
  const report = { scope: 'Local fixture / isolated headless Chrome. Pairing and transfer simulated; no physical binding or display evidence.', checks, findings, captures, layouts, errors, requests: state };
  await writeFile(path.join(output, 'report.json'), JSON.stringify(report, null, 2) + '\n');
  if (browserCDP) { try { await browserCDP.call('Browser.close'); } catch { /* Chrome may close before acknowledging. */ } }
  page?.close(); browserCDP?.close();
  if (browser && browser.exitCode === null) { browser.kill('SIGTERM'); await Promise.race([once(browser, 'exit'), pause(3000)]); }
  await new Promise(resolve => server.close(resolve));
  await Promise.all([rm(profile, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 }), rm(downloads, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 })]);
}
console.log(`UI evidence: ${output}/report.json`);
if (findings.length) process.exitCode = 1;
