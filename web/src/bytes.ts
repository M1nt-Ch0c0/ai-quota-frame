export function concat(...parts: Uint8Array[]): Uint8Array {
  const result = new Uint8Array(parts.reduce((n, part) => n + part.length, 0));
  let offset = 0;
  for (const part of parts) { result.set(part, offset); offset += part.length; }
  return result;
}
export const utf8 = (value: string) => new TextEncoder().encode(value);
export const hex = (value: Uint8Array) => Array.from(value, b => b.toString(16).padStart(2, '0')).join('');
export function unhex(value: string, size: number): Uint8Array {
  if (!new RegExp(`^[0-9a-f]{${size * 2}}$`, 'i').test(value)) throw new Error('事务标识格式错误');
  return Uint8Array.from(value.match(/../g)!, pair => parseInt(pair, 16));
}
export const view = (data: Uint8Array) => new DataView(data.buffer, data.byteOffset, data.byteLength);
export function u32(value: number): Uint8Array {
  const result = new Uint8Array(4); view(result).setUint32(0, value, true); return result;
}
export function equal(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  let difference = 0; for (let i = 0; i < a.length; i++) difference |= a[i] ^ b[i];
  return difference === 0;
}
export const sha256 = async (value: Uint8Array) => new Uint8Array(await crypto.subtle.digest('SHA-256', value as BufferSource));
export function check(signal: AbortSignal): void { signal.throwIfAborted(); }
export async function sleep(ms: number, signal: AbortSignal): Promise<void> {
  check(signal);
  await new Promise<void>((resolve, reject) => {
    const abort = () => { clearTimeout(timer); reject(signal.reason); };
    const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve(); }, ms);
    signal.addEventListener('abort', abort, { once: true });
  });
}
export function deadline(signal: AbortSignal, ms: number): AbortSignal {
  return AbortSignal.any([signal, AbortSignal.timeout(ms)]);
}
