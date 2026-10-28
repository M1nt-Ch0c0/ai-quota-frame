import { collectSnapshot, DEFAULT_FRAME_OPTIONS, forgetSourceCredentials, frameFingerprint, type FrameOptions, type FrameSnapshot } from './frame-bridge.ts';
import { renderSnapshot } from './render.ts';
import { GattLink, HOST_SERVICE } from './ble/gatt-link.ts';
import { DeviceError, parseInfo, Session, targetFromStatus } from './ble/session.ts';
import { IndexedBondStore, type BondSummary } from './ble/bonds.ts';
import { connectPaired, grantedDevices, pairDevice, PairingError, type PairingProgress } from './ble/pairing.ts';
import { lockDevice } from './lock.ts';
import { Sender, PendingError, UnknownOutcomeError } from './photoframe/sender.ts';
import { IndexedPendingStore, type Pending } from './photoframe/store.ts';
import { MAX_PNG, validatePNG } from './png.ts';
import { hex, sha256, sleep } from './bytes.ts';

const el = <T extends HTMLElement = HTMLElement>(id: string) => {
  const value = document.getElementById(id);
  if (!value) throw new Error('页面缺少必要控件：' + id);
  return value as T;
};
const btn = (id: string) => el<HTMLButtonElement>(id);
const input = (id: string) => el<HTMLInputElement>(id);
const dlg = (id: string) => el<HTMLDialogElement>(id);
const text = (id: string, value: string) => { el(id).textContent = value; };
const show = (id: string, value: boolean) => { el(id).hidden = !value; };
const errorText = (error: unknown) => error instanceof DeviceError ? ({
  1: '相框未认可这次连接，请重新配对。', 2: '相框尚未配对。', 3: '相框正在处理另一项操作。',
  4: '相框未能处理请求，请重新连接。', 8: '相框当前状态不允许这项操作。',
  10: '相框未能保存配对信息。', 11: '连接已过期，请重新连接。',
  13: '相框未能完成屏幕刷新。', 14: '相框已重启，请先核对上次显示结果。',
} as Record<number, string>)[error.code] || '相框未完成这项操作，请重新连接。' : error instanceof Error ? error.message : '操作未完成，请重试';
const themes = ['classic', 'mint', 'arcade', 'pop', 'mint-skin'] as const;
const names: Record<string, string> = { classic: '经典', mint: '薄荷', arcade: '街机', pop: '波普', 'mint-skin': '薄荷皮肤' };
const providerValues = Array.from(document.querySelectorAll<HTMLInputElement>('#providers input')).map(v => v.value);
const preferencesKey = 'photopainter:display-v2', selectionKey = 'photopainter:selected-v2';
interface Preferences extends Required<FrameOptions> { automatic: boolean; }
function loadPreferences(): Preferences {
  const defaults = { ...DEFAULT_FRAME_OPTIONS, automatic: false };
  try {
    const p = JSON.parse(localStorage.getItem(preferencesKey) || 'null');
    if (!p || !themes.includes(p.theme) || typeof p.providers !== 'string' || !p.providers.length || p.providers.split(',').length > 5 ||
      new Set(p.providers.split(',')).size !== p.providers.split(',').length ||
      !p.providers.split(',').every((v: string) => providerValues.includes(v)) || typeof p.timezone !== 'string' ||
      !Number.isInteger(p.refreshSeconds) || p.refreshSeconds < 60 || p.refreshSeconds > 86400) return defaults;
    new Intl.DateTimeFormat('zh-CN', { timeZone: p.timezone });
    return { theme: p.theme, providers: p.providers, timezone: p.timezone, refreshSeconds: p.refreshSeconds, automatic: p.automatic === true };
  } catch { return defaults; }
}
let preferences = loadPreferences(), managementKey = '', selectedID = '', imageURL = '';
const transactions = new IndexedPendingStore(), bindings = new IndexedBondStore(), sender = new Sender(transactions);
let bonds: BondSummary[] = [], pending: Pending | undefined, storageReady = false;
const granted = new Map<string, BluetoothDevice>(), sent = new Map<string, string>();
const cancelledAutomaticImages = new Set<string>();
interface Image { bytes: Uint8Array; at: string; key: string; }
interface Connection { device: BluetoothDevice; link: GattLink; session: Session; release: () => void; ready: boolean; }
interface PairAttempt { abort: AbortController; cancel: AbortController; stage: string; }
let connection: Connection | undefined, pairing: PairAttempt | undefined;
let prepared: Image | undefined, snapshot: FrameSnapshot | undefined, queued: FrameSnapshot | undefined;
let sourceWork: AbortController | undefined, renderWork: AbortController | undefined, transferWork: AbortController | undefined;
let renderEpoch = 0, transferStage = '', forgetUntil = 0, clearUntil = 0;
let autoNotBefore = 0;
let activeSendKey = '';
let sourceState: 'missing' | 'loading' | 'ready' | 'error' = 'missing';
let refreshTimer: ReturnType<typeof setTimeout> | undefined, autoTimer: ReturnType<typeof setTimeout> | undefined;
const supported = isSecureContext && !!navigator.bluetooth && !!crypto.subtle;
const options = (): FrameOptions => ({ theme: preferences.theme, providers: preferences.providers, timezone: preferences.timezone, refreshSeconds: preferences.refreshSeconds });
const selected = () => bonds.find(b => b.deviceID === selectedID);
function time(value: string): string {
  const date = new Date(value); if (!Number.isFinite(date.getTime())) return '';
  return new Intl.DateTimeFormat('zh-CN', { timeZone: preferences.timezone, month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(date);
}
function say(title: string, detail = '', tone = 'neutral') {
  text('status-text', title); text('detail', detail); show('detail', !!detail);
  el('status-text').closest<HTMLElement>('.pp-transfer')!.dataset.tone = tone;
}
function fieldError(id: string, error?: unknown) { text(id, error ? errorText(error) : ''); show(id, !!error); }
function validateProviderSelection(): boolean {
  const count = document.querySelectorAll('#providers input:checked').length;
  const valid = count >= 1 && count <= 5;
  btn('settings-save').disabled = !valid; el('providers').toggleAttribute('data-invalid', !valid);
  text('providers-hint', count > 5 ? '最多选择 5 个平台。' : '选择 1–5 个平台。');
  return valid;
}
function open(id: string, focus: string) { if (!dlg(id).open) { dlg(id).showModal(); el(focus).focus(); } }
function close(id: string) { if (dlg(id).open) dlg(id).close(); }
function select(id: string) { selectedID = id; try { localStorage.setItem(selectionKey, id); } catch { /* Optional preference. */ } }
function update() {
  const busy = !!pairing || !!transferWork, saved = selected();
  btn('read-cpm').disabled = !!sourceWork; btn('read-cpm').textContent = sourceWork ? '正在更新…' : '更新画面';
  btn('view-settings').disabled = !!transferWork || !!renderWork; btn('source-settings').disabled = !!sourceWork;
  btn('source-save').disabled = !!sourceWork; input('panel-key').disabled = !!sourceWork;
  btn('connect').disabled = busy || !!connection || !supported || !storageReady;
  btn('connect').textContent = pairing ? '正在连接…' : saved ? '连接相框' : '添加相框';
  btn('add-device').disabled = busy || !!connection || !supported || !storageReady; show('add-device', bonds.length > 0);
  show('disconnect', !!connection); btn('disconnect').disabled = busy;
  show('prepare-frame', !!connection && !connection.ready); btn('prepare-frame').disabled = busy;
  btn('send').disabled = !prepared || !!snapshot?.stale || !connection?.ready || busy || !!sourceWork || !!renderWork || !!pending;
  btn('recover').disabled = busy || !storageReady || !supported;
  btn('acknowledge').disabled = busy || !!connection; btn('forget').disabled = busy || !!connection || !saved;
  btn('download').disabled = !prepared; show('cancel', !!transferWork); btn('cancel').disabled = !transferWork;
  btn('cancel').textContent = transferStage === 'displaying' ? '停止等待' : '取消发送';
  btn('load-json').disabled = !!transferWork || !el<HTMLTextAreaElement>('snapshot-json').value.trim();
  btn('load-png').disabled = !!transferWork; el<HTMLSelectElement>('device-select').disabled = busy || !!connection;
  show('device-select', bonds.length > 1); show('pending', !!pending);
  text('pending-detail', pending ? time(pending.createdAt) + ' 的发送结果尚未确认。' : '');
  el('source-settings').dataset.state = sourceState;
  text('source-state', ({ missing: '未设置', loading: '更新中', ready: '已连接', error: '需检查' })[sourceState]);
  btn('clear-panel-key').disabled = !managementKey && !input('panel-key').value;
  el('device').dataset.state = pairing ? 'connecting' : connection ? (connection.ready ? 'connected' : 'busy') : saved ? 'offline' : 'none';
  show('device-state', !!saved || !!connection || !!pairing);
  text('device-state', pairing ? '正在连接' : connection ? (connection.ready ? '已连接' : '准备中') : '未连接');
  text('device-name', connection?.device.name || saved?.name || '尚未添加相框'); show('device-info', !!saved);
  text('device-id', saved?.deviceID || ''); text('device-fingerprint', saved?.bindingID || '');
  text('device-paired-at', saved?.pairedAt ? time(saved.pairedAt) : '');
  for (const id of ['read-cpm', 'connect', 'send', 'recover']) btn(id).classList.remove('is-primary');
  btn(pending ? 'recover' : !prepared ? 'read-cpm' : !connection ? 'connect' : 'send').classList.add('is-primary');
}
async function refreshRecords() {
  try {
    [bonds, pending] = await Promise.all([bindings.list(), transactions.get()]); storageReady = true;
    if (!selectedID) try { selectedID = localStorage.getItem(selectionKey) || ''; } catch { /* Optional. */ }
    if (!bonds.some(b => b.deviceID === selectedID)) selectedID = bonds[0]?.deviceID || '';
    const select = el<HTMLSelectElement>('device-select'); select.replaceChildren();
    for (const bond of bonds) { const option = new Option(bond.name || 'PhotoPainter ' + bond.deviceID.slice(-4), bond.deviceID); option.selected = bond.deviceID === selectedID; select.add(option); }
  } catch (error) { storageReady = false; say('无法读取本机记录', errorText(error), 'error'); }
  update();
}
function disconnect() {
  const old = connection; connection = undefined;
  if (old) { old.session.disconnect(); old.release(); }
  update();
}
function invalidate() { prepared = undefined; queued = undefined; if (snapshot) snapshot = { ...snapshot, stale: true }; renderEpoch++; update(); }
function scheduleRefresh() {
  if (refreshTimer) clearTimeout(refreshTimer);
  if (managementKey) refreshTimer = setTimeout(() => { void refreshSource(); }, preferences.refreshSeconds * 1000);
}
async function installImage(bytes: Uint8Array, at: string, key: string, epoch: number) {
  await validatePNG(bytes); if (epoch !== renderEpoch) return;
  if (imageURL) URL.revokeObjectURL(imageURL);
  prepared = { bytes: bytes.slice(), at, key }; imageURL = URL.createObjectURL(new Blob([prepared.bytes as BlobPart], { type: 'image/png' }));
  const preview = el<HTMLImageElement>('preview'); preview.src = imageURL;
  preview.alt = '相框预览：' + names[preferences.theme] + '，数据时间 ' + time(at);
  show('preview', true); show('empty-preview', false); text('image-meta', '800 × 480 · ' + names[preferences.theme]); show('image-meta', true);
  text('snapshot-time', '数据时间 ' + time(at)); show('snapshot-time', true); el<HTMLTimeElement>('snapshot-time').dateTime = at; update();
}
async function prepare(value: FrameSnapshot) {
  const epoch = ++renderEpoch, work = new AbortController(); renderWork?.abort(); renderWork = work;
  el('frame').setAttribute('aria-busy', 'true');
  try {
    const key = await frameFingerprint(value) + ':' + JSON.stringify(options());
    if (prepared?.key !== key) await installImage(await renderSnapshot(value, options(), work.signal), value.data_updated_at || value.refreshed_at, key, epoch);
    if (epoch !== renderEpoch) return;
    snapshot = value;
    if (value.stale) { queued = undefined; say('显示上次已知数据', '数据源暂时不可用，稍后会再更新。', 'error'); }
    else say('画面已就绪', connection?.ready ? '可以发送到相框。' : '连接相框后即可发送。');
  } catch (error) { if (!work.signal.aborted) { invalidate(); say('画面生成失败', errorText(error), 'error'); } throw error; }
  finally { if (renderWork === work) { renderWork = undefined; el('frame').setAttribute('aria-busy', 'false'); } update(); }
}
async function refreshSource() {
  if (sourceWork) return;
  if (!managementKey) { open('source-dialog', 'panel-key'); return; }
  const work = new AbortController(); sourceWork = work; sourceState = 'loading'; update(); fieldError('source-error');
  if (!transferWork) say('正在更新画面');
  try {
    const next = await collectSnapshot(managementKey, work.signal, options());
    const known = next.accounts.some(account => account.windows.some(window =>
      typeof window.remaining_percent === 'number' || typeof window.used_percent === 'number')) || !!next.usage?.days?.length;
    if (next.stale && !known) throw new Error('尚未读取到额度，请检查数据源后重试。');
    sourceState = next.stale ? 'error' : 'ready';
    if (transferWork) { queued = next.stale ? undefined : next; if (next.stale) prepared = undefined; }
    else await prepare(next);
    if (!next.stale) { close('source-dialog'); input('panel-key').value = ''; input('panel-key').placeholder = '已设置，输入新密钥可替换'; }
  } catch (error) {
    if (!work.signal.aborted) { sourceState = 'error'; invalidate(); fieldError('source-error', error); if (!transferWork) say('更新未完成', errorText(error), 'error'); }
  } finally { if (sourceWork === work) sourceWork = undefined; update(); scheduleRefresh(); maybeAuto(); }
}
function pairProgress(progress: PairingProgress) {
  if (!pairing) return; pairing.stage = progress.stage;
  if (progress.stage === 'negotiating' && !dlg('pair-dialog').open) return;
  const code = progress.code;
  if (code || progress.stage === 'saving') open('pair-dialog', 'pair-cancel');
  dlg('pair-dialog').dataset.state = progress.stage === 'paired' ? 'success' : progress.stage === 'confirm-on-frame' ? 'waiting' : progress.stage === 'displaying' ? 'showing' : 'connecting';
  text('pair-title', progress.stage === 'paired' ? '配对完成' : code ? '核对配对码' : '正在连接');
  text('pair-instruction', progress.stage === 'paired' ? '以后可直接连接这台相框。' : progress.stage === 'confirm-on-frame' ? '数字一致时，按一下相框的功能键。' : code ? '相框正在显示配对码。' : '正在与相框协商连接。');
  el('pair-code').querySelector('.pp-code-digits')!.textContent = code ? code.slice(0, 3) + ' ' + code.slice(3) : '';
  el('pair-code').querySelector('.pp-code-spoken')!.textContent = code ? code.split('').join(' ') : '';
  show('pair-code', !!code); show('pair-complete', progress.stage === 'paired'); show('pair-cancel', progress.stage !== 'paired'); show('pair-retry', false); fieldError('pair-error');
  if (progress.stage === 'paired') btn('pair-complete').focus();
}
function pairFailure(error: unknown) {
  open('pair-dialog', 'pair-retry'); dlg('pair-dialog').dataset.state = 'error'; text('pair-title', '未能连接');
  show('pair-code', false); show('pair-retry', true); show('pair-complete', false); show('pair-cancel', false); fieldError('pair-error', error);
  text('pair-instruction', error instanceof PairingError && ['owned', 'fault'].includes(error.kind) ? '在相框上长按功能键 8 秒，再重新连接。' : '确认相框在附近后重试。'); btn('pair-retry').focus();
}
async function inspectFrame(session: Session, signal: AbortSignal): Promise<boolean> {
  const until = Date.now() + 90000;
  for (;;) {
    try {
      const status = await session.status(signal);
      text('device-firmware', typeof status.version === 'number' && status.version > 0 ? 'photoframe v' + status.version : '');
      try { targetFromStatus(status); return true; } catch { return false; }
    } catch (error) { if (!(error instanceof DeviceError) || error.code !== 3 || Date.now() >= until) throw error; await sleep(500, signal); }
  }
}
interface ConnectOptions { add?: boolean; quiet?: boolean; forPending?: boolean; }
function connectDevice(opts: ConnectOptions = {}): Promise<boolean> {
  if (connection) return Promise.resolve(true);
  if (!supported || !storageReady || pairing || transferWork) return Promise.resolve(false);
  const preferred = opts.forPending && pending ? bonds.find(b => b.deviceID === pending!.deviceID) : selected();
  const cached = !opts.add && preferred ? granted.get(preferred.browserID) : undefined;
  if (opts.quiet && (!preferred || preferred.phase !== 'paired' || !cached)) return Promise.resolve(false);
  // Call the browser picker synchronously from the user gesture.
  const chosen = cached ? Promise.resolve(cached) : navigator.bluetooth.requestDevice({ filters: [{ services: [HOST_SERVICE] }] });
  const attempt: PairAttempt = { abort: new AbortController(), cancel: new AbortController(), stage: 'negotiating' }; pairing = attempt; btn('pair-cancel').disabled = false; update();
  return (async () => {
    let link: GattLink | undefined, release: (() => void) | undefined;
    try {
      const device = await chosen; attempt.abort.signal.throwIfAborted(); granted.set(device.id, device);
      if (!preferred || preferred.phase === 'pending' || opts.add) { open('pair-dialog', 'pair-cancel'); pairProgress({ stage: 'negotiating', deviceID: '' }); }
      text('pair-device-name', device.name || 'PhotoPainter 相框'); release = await lockDevice(device.id);
      link = await GattLink.connect(device, AbortSignal.any([attempt.abort.signal, AbortSignal.timeout(30000)]));
      const info = parseInfo(await link.info(attempt.abort.signal));
      if (!opts.add && preferred && preferred.deviceID !== info.id) throw new Error('请选择原来的相框，或使用“添加”连接另一台。');
      const saved = await bindings.get(info.id);
      const session = opts.quiet && saved?.phase === 'paired'
        ? await connectPaired(link, saved, bindings, attempt.abort.signal)
        : await pairDevice(link, { deviceID: info.id, browserID: device.id, name: device.name || 'PhotoPainter ' + info.id.slice(-4), clientName: 'PhotoPainter Browser' }, bindings, attempt.abort.signal, pairProgress, attempt.cancel.signal);
      if (dlg('pair-dialog').open) {
        pairProgress({ stage: 'paired', deviceID: info.id });
        if (saved?.phase === 'paired') text('pair-title', '相框已连接');
      }
      select(info.id); connection = { device, link, session, release, ready: false }; release = undefined;
      device.addEventListener('gattserverdisconnected', () => { if (connection?.device === device && !transferWork && !pairing) { disconnect(); say('相框已断开', '点击连接即可继续。'); } }, { once: true });
      await refreshRecords(); connection.ready = await inspectFrame(session, attempt.abort.signal);
      say(connection.ready ? '相框已连接' : '相框应用未就绪', connection.ready ? '' : '点击“打开相框应用”继续。'); return true;
    } catch (error) {
      if (connection) disconnect(); else link?.disconnect(); release?.();
      if (error instanceof DOMException && error.name === 'NotFoundError') { close('pair-dialog'); say('未选择相框'); }
      else if (attempt.cancel.signal.aborted || attempt.abort.signal.aborted) { close('pair-dialog'); say('连接已停止'); }
      else if (opts.quiet) say('相框尚未连接', '点击连接相框继续。'); else pairFailure(error);
      return false;
    } finally { if (pairing === attempt) pairing = undefined; await refreshRecords(); update(); }
  })();
}
async function transfer(recover: boolean, automatic = false) {
  if (!connection || pairing || transferWork || !storageReady) return;
  const current = connection, image = prepared;
  if (!recover && (!image || snapshot?.stale || !current.ready || pending || sourceWork || renderWork)) return;
  const scope = current.session.info.id + ':' + current.session.bindingID;
  const recoveryRecord = pending;
  activeSendKey = !recover && image ? scope + ':' + image.key : '';
  if (!automatic && activeSendKey) cancelledAutomaticImages.delete(activeSendKey);
  const work = new AbortController(); transferWork = work; transferStage = recover ? 'displaying' : 'uploading'; update();
  const progress = (stage: 'uploading' | 'displaying' | 'complete', received: number, total: number) => {
    transferStage = stage; const bar = el<HTMLProgressElement>('progress'); show('progress', stage !== 'complete');
    if (stage === 'displaying') bar.removeAttribute('value'); else bar.value = total ? received * 100 / total : 0;
    text('progress-text', stage === 'uploading' ? Math.floor(received * 100 / total) + '%' : '');
    if (stage === 'complete') say('相框已显示新画面', '完成于 ' + time(new Date().toISOString()), 'success');
    else say(stage === 'displaying' ? '相框正在刷新' : '正在发送画面'); update();
  };
  try {
    if (recover) {
      await sender.recover(current.session, work.signal, progress);
      if (image && recoveryRecord && hex(await sha256(image.bytes)) === recoveryRecord.digest) sent.set(scope, image.key);
    }
    else { await sender.send(current.session, image!.bytes, image!.at, work.signal, progress); sent.set(scope, image!.key); autoNotBefore = 0; }
  } catch (error) {
    autoNotBefore = Date.now() + 30000;
    if (error instanceof UnknownOutcomeError) say('发送结果需要核对', '请先查看相框画面，再处理这条记录。', 'error');
    else if (error instanceof PendingError) say('发送结果待确认', '重新连接后可核对原结果。');
    else if (work.signal.aborted) say('已停止等待', '可重新连接核对结果。');
    else say('发送未完成', errorText(error), 'error');
  } finally {
    disconnect(); transferWork = undefined; transferStage = ''; activeSendKey = ''; await refreshRecords(); update();
    const latest = queued; queued = undefined;
    if (latest && !pending) try { await prepare(latest); } catch { /* State already shown. */ }
    maybeAuto();
  }
}
function maybeAuto() {
  if (autoTimer) clearTimeout(autoTimer);
  if (!preferences.automatic || !prepared || snapshot?.stale || pending || sourceWork || renderWork || pairing || transferWork || !storageReady) return;
  const saved = selected();
  if (saved && sent.get(saved.deviceID + ':' + saved.bindingID) === prepared.key) return;
  if (saved && cancelledAutomaticImages.has(saved.deviceID + ':' + saved.bindingID + ':' + prepared.key)) return;
  autoTimer = setTimeout(() => { void (async () => {
    if (!preferences.automatic || !prepared || pending || transferWork || pairing) return;
    if (!connection && !await connectDevice({ quiet: true })) return;
    if (connection?.ready && prepared && sent.get(connection.session.info.id + ':' + connection.session.bindingID) !== prepared.key) await transfer(false, true);
  })(); }, Math.max(0, autoNotBefore - Date.now()));
}
function cancelPair() {
  if (!pairing || pairing.stage === 'paired') { close('pair-dialog'); return; }
  pairing.cancel.abort(); btn('pair-cancel').disabled = true; text('pair-instruction', '正在取消配对…');
}
btn('connect').onclick = () => { void connectDevice().then(ok => { if (ok) maybeAuto(); }); };
btn('add-device').onclick = () => { void connectDevice({ add: true }); };
btn('pair-retry').onclick = () => { void connectDevice({ add: !selected() }); };
btn('pair-cancel').onclick = cancelPair; btn('pair-close').onclick = cancelPair;
dlg('pair-dialog').addEventListener('cancel', event => { event.preventDefault(); cancelPair(); });
btn('pair-complete').onclick = () => close('pair-dialog');
btn('disconnect').onclick = () => { disconnect(); say('已断开相框'); };
el<HTMLSelectElement>('device-select').onchange = event => { select((event.target as HTMLSelectElement).value); forgetUntil = 0; update(); };
btn('send').onclick = () => { void transfer(false); };
btn('recover').onclick = () => {
  if (connection && pending && connection.session.info.id !== pending.deviceID) disconnect();
  void connectDevice({ forPending: true }).then(ok => { if (ok) void transfer(true); });
};
btn('cancel').onclick = () => {
  if (activeSendKey) cancelledAutomaticImages.add(activeSendKey);
  if (transferStage === 'uploading') sender.cancelUpload(); else transferWork?.abort();
};
btn('prepare-frame').onclick = async () => {
  if (!connection || pairing || transferWork) return;
  const current = connection, work = new AbortController(); transferWork = work; update(); say('正在打开相框应用');
  try {
    const app = new Uint8Array(32); app.set(new TextEncoder().encode('photoframe'));
    try { await current.session.busyCommand(10, app, AbortSignal.any([work.signal, AbortSignal.timeout(11 * 60 * 1000)]), true); }
    catch { disconnect(); throw new Error('启动结果待核对。重新连接后读取状态即可。'); }
    current.ready = await inspectFrame(current.session, work.signal); say(current.ready ? '相框已就绪' : '相框应用未就绪');
  } catch (error) { say('暂未确认相框状态', errorText(error), 'error'); }
  finally { transferWork = undefined; update(); }
};
btn('forget').onclick = async () => {
  const bond = selected(); if (!bond || connection || pairing || transferWork) return;
  if (Date.now() > forgetUntil) { forgetUntil = Date.now() + 5000; btn('forget').textContent = '确认移除此浏览器的记忆'; return; }
  let release: (() => void) | undefined;
  try { release = await lockDevice(bond.browserID); await bindings.forget(bond.deviceID); select(''); say('已移除相框', '在相框上长按功能键 8 秒后，可重新配对。'); }
  catch (error) { say('未能移除相框', errorText(error), 'error'); }
  finally { release?.(); forgetUntil = 0; btn('forget').textContent = '移除此相框'; await refreshRecords(); }
};
btn('acknowledge').onclick = async () => {
  if (!pending || connection || pairing || transferWork) return;
  if (Date.now() > clearUntil) { clearUntil = Date.now() + 5000; btn('acknowledge').textContent = '已查看相框，确认清除'; return; }
  let release: (() => void) | undefined;
  try { release = await lockDevice(pending.deviceID); await transactions.remove(pending); say('已清除待确认记录'); }
  catch (error) { say('记录未清除', errorText(error), 'error'); }
  finally { release?.(); clearUntil = 0; btn('acknowledge').textContent = '核对画面后清除'; await refreshRecords(); }
};
btn('read-cpm').onclick = () => { void refreshSource(); };
btn('source-settings').onclick = () => { fieldError('source-error'); open('source-dialog', 'panel-key'); };
btn('source-close').onclick = () => close('source-dialog');
dlg('source-dialog').querySelector('form')!.addEventListener('submit', event => {
  event.preventDefault(); const value = input('panel-key').value.trim(); if (value) managementKey = value;
  if (!managementKey) { fieldError('source-error', new Error('请输入管理密钥。')); return; } void refreshSource();
});
input('panel-key').oninput = update;
btn('clear-panel-key').onclick = () => { sourceWork?.abort(); managementKey = ''; sourceState = 'missing'; input('panel-key').value = ''; input('panel-key').placeholder = ''; forgetSourceCredentials(); invalidate(); scheduleRefresh(); update(); };
btn('view-settings').onclick = () => {
  el<HTMLSelectElement>('theme').value = preferences.theme; el<HTMLSelectElement>('timezone').value = preferences.timezone;
  el<HTMLSelectElement>('refresh-seconds').value = String(preferences.refreshSeconds); input('automatic').checked = preferences.automatic;
  for (const value of document.querySelectorAll<HTMLInputElement>('#providers input')) value.checked = preferences.providers.split(',').includes(value.value);
  validateProviderSelection();
  open('settings-dialog', 'theme');
};
btn('settings-close').onclick = () => close('settings-dialog');
el('providers').onchange = validateProviderSelection;
dlg('settings-dialog').querySelector('form')!.addEventListener('submit', event => {
  event.preventDefault(); if (!validateProviderSelection()) return;
  const providers = Array.from(document.querySelectorAll<HTMLInputElement>('#providers input:checked')).map(v => v.value).join(',');
  preferences = { theme: el<HTMLSelectElement>('theme').value as Preferences['theme'], providers, timezone: el<HTMLSelectElement>('timezone').value, refreshSeconds: Number(el<HTMLSelectElement>('refresh-seconds').value), automatic: input('automatic').checked };
  try { localStorage.setItem(preferencesKey, JSON.stringify(preferences)); } catch { /* Keep this page's settings. */ }
  close('settings-dialog'); scheduleRefresh(); if (snapshot) void prepare(snapshot).then(maybeAuto).catch(() => {});
});
btn('download').onclick = () => { if (prepared && imageURL) { const a = document.createElement('a'); a.href = imageURL; a.download = 'photopainter.png'; a.click(); } };
el<HTMLTextAreaElement>('snapshot-json').oninput = update;
btn('choose-json').onclick = () => input('snapshot-file').click();
input('snapshot-file').onchange = async () => { const file = input('snapshot-file').files?.[0]; if (file && file.size <= 4 * 1024 * 1024) { el<HTMLTextAreaElement>('snapshot-json').value = await file.text(); update(); } };
btn('load-json').onclick = async () => {
  try { const value = JSON.parse(el<HTMLTextAreaElement>('snapshot-json').value) as FrameSnapshot;
    if (value.schema_version !== 1 || !Array.isArray(value.accounts) || !value.data_updated_at) throw new Error('请选择 ai-quota-frame 的完整快照。');
    await prepare(value); close('source-dialog'); fieldError('snapshot-error');
  } catch (error) { invalidate(); fieldError('snapshot-error', error); }
};
btn('load-png').onclick = () => input('file').click();
input('file').onchange = async () => {
  const file = input('file').files?.[0]; if (!file) return; const epoch = ++renderEpoch;
  try { if (file.size > MAX_PNG) throw new Error('图片超过相框可接收的大小。'); await installImage(new Uint8Array(await file.arrayBuffer()), new Date().toISOString(), 'import:' + crypto.randomUUID(), epoch); snapshot = undefined; close('source-dialog'); say('图片已载入'); }
  catch (error) { invalidate(); fieldError('snapshot-error', error); }
};
window.addEventListener('pagehide', () => {
  sourceWork?.abort(); renderWork?.abort(); pairing?.abort.abort(); transferWork?.abort(); disconnect();
  managementKey = ''; sourceState = 'missing'; input('panel-key').value = ''; forgetSourceCredentials();
  if (refreshTimer) clearTimeout(refreshTimer); if (autoTimer) clearTimeout(autoTimer);
});
window.addEventListener('pageshow', () => { void refreshRecords(); });
if (!supported) say('此浏览器无法连接相框', '请使用支持网页蓝牙的 Chrome 打开 HTTPS 页面。');
void (async () => { await refreshRecords(); try { for (const device of await grantedDevices()) granted.set(device.id, device); } catch { /* Picker still available. */ } update(); })();
update();
