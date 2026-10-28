export async function lockDevice(_id: string): Promise<() => void> {
  if (!navigator.locks) throw new Error('当前浏览器无法独占相框连接，请更新 Chrome 后重试');
  let release: () => void = () => {};
  const held = new Promise<void>(resolve => { release = resolve; });
  return new Promise((resolve, reject) => {
    // The MVP has one durable pending slot. Cover the complete connection and
    // transaction across ALL devices, before touching GATT, not just Send.
    void navigator.locks.request('photopainter:session', { mode: 'exclusive', ifAvailable: true }, async lock => {
      if (!lock) { reject(new Error('另一标签页正在使用该相框，请先断开它')); return; }
      resolve(release); await held;
    }).catch(reject);
  });
}
