// A visible-page screen lock only; it cannot run BLE after the page is closed.
export class ScreenAwake {
  private lock?: WakeLockSentinel;
  private requested?: Promise<void>;
  private wanted = false;
  private failure = '';
  private readonly changed: () => void;
  constructor(changed: () => void) { this.changed = changed; }
  get active(): boolean { return !!this.lock && !this.lock.released; }
  get error(): string { return this.failure; }
  async set(enabled: boolean): Promise<void> {
    this.wanted = enabled;
    if (!enabled || document.visibilityState !== 'visible') {
      if (this.lock) { const lock = this.lock; this.lock = undefined; await lock.release(); }
      this.changed(); return;
    }
    if (this.active || this.requested) return this.requested;
    if (!navigator.wakeLock) { this.failure = '此浏览器不支持屏幕常亮，请保持屏幕唤醒。'; this.changed(); return; }
    this.requested = (async () => {
      try {
        const lock = await navigator.wakeLock.request('screen');
        if (!this.wanted || document.visibilityState !== 'visible') { await lock.release(); return; }
        this.lock = lock; this.failure = '';
        lock.addEventListener('release', () => { if (this.lock === lock) { this.lock = undefined; if (this.wanted && document.visibilityState === 'visible') this.failure = '系统已释放屏幕唤醒，请手动保持屏幕开启。'; } this.changed(); }, { once: true });
      } catch (error) { this.failure = '未能保持屏幕唤醒：' + (error instanceof Error ? error.message : '系统未允许') + '。请手动保持屏幕开启。'; }
      finally { this.requested = undefined; this.changed(); }
    })();
    return this.requested;
  }
}
