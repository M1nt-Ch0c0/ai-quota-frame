// Same-origin CPM storage contract; see CPM's obfuscatedStorage/useAuthStore.
// This format is reversible obfuscation, not encryption.
export const CPM_AUTH_KEY = 'cli-proxy-auth';
export const SOURCE_AUTH_KEY = 'photopainter:source-auth-v1';
type Store = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
export interface AuthContext { origin: string; host: string; userAgent: string; }
export interface SourceCredential { key: string; source: 'cpm' | 'saved'; }
function transform(bytes: Uint8Array, context: AuthContext, version: number): Uint8Array {
  const suffix = version === 1 ? `${context.host}|${context.userAgent}` : `v2|${context.host}`;
  const mask = new TextEncoder().encode(`cli-proxy-api-webui::secure-storage|${suffix}`);
  return bytes.map((value, index) => value ^ mask[index % mask.length]);
}
export function decodeStoredAuth(raw: string, context: AuthContext): unknown {
  try {
    const match = /^enc::v([12])::/.exec(raw);
    const text = match ? new TextDecoder('utf-8', { fatal: true }).decode(transform(
      Uint8Array.from(atob(raw.slice(match[0].length)), c => c.charCodeAt(0)), context, Number(match[1]))) : raw;
    return JSON.parse(text);
  } catch { throw new Error('无法读取已保存的登录配置，请在 CPM 重新登录或清除本插件的手动配置。'); }
}
function validKey(value: unknown): value is string {
  return typeof value === 'string' && value.trim().length > 0 && value.length <= 4096 && !/[\r\n]/.test(value);
}
export function loadSourceCredential(store: Store, context: AuthContext): SourceCredential | undefined {
  const cpm = store.getItem(CPM_AUTH_KEY);
  if (cpm) {
    const record = decodeStoredAuth(cpm, context) as { state?: { managementKey?: unknown; apiBase?: unknown } };
    if (validKey(record?.state?.managementKey)) {
      const base = record.state.apiBase;
      if (typeof base !== 'string' || new URL(base, context.origin).origin !== context.origin)
        throw new Error('CPM 当前连接的是其他服务器，请打开与本插件同源的管理中心。');
      return { key: record.state.managementKey.trim(), source: 'cpm' };
    }
  }
  const saved = store.getItem(SOURCE_AUTH_KEY);
  if (!saved) return undefined;
  const record = decodeStoredAuth(saved, context) as { origin?: unknown; key?: unknown };
  if (record?.origin !== context.origin || !validKey(record?.key)) throw new Error('本插件保存的数据源配置无效，请清除后重新设置。');
  return { key: record.key.trim(), source: 'saved' };
}
export function saveSourceCredential(store: Store, context: AuthContext, key: string): void {
  if (!validKey(key)) throw new Error('请输入有效的 CPM 管理密钥。');
  const bytes = transform(new TextEncoder().encode(JSON.stringify({ origin: context.origin, key: key.trim() })), context, 2);
  store.setItem(SOURCE_AUTH_KEY, 'enc::v2::' + btoa(String.fromCharCode(...bytes)));
}
export function clearSourceCredential(store: Store): void { store.removeItem(SOURCE_AUTH_KEY); }
