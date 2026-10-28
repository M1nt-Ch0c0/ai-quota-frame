import { frameHTML, quantizeFrame, type ArtworkBounds, type FrameOptions, type FrameSnapshot } from './frame-bridge.ts';
import { HEIGHT, WIDTH } from './png.ts';
export interface FrameRaster { png: Uint8Array; bounds: ArtworkBounds[]; }

// Use the original Go HTML/CSS document for layout. The self-contained SVG
// carries that same layout to Canvas; there are no parallel frame-design rules.
export async function rasterFrameHTML(html: string, signal?: AbortSignal): Promise<FrameRaster> {
  signal?.throwIfAborted();
  const frame = document.createElement('iframe');
  frame.sandbox.add('allow-same-origin'); frame.setAttribute('aria-hidden', 'true'); frame.tabIndex = -1;
  Object.assign(frame.style, { width: `${WIDTH}px`, height: `${HEIGHT}px`, position: 'fixed', left: '-10000px', top: '0', border: '0', pointerEvents: 'none' });
  const loaded = new Promise<void>((resolve, reject) => {
    frame.onload = () => resolve(); frame.onerror = () => reject(new Error('无法排版相框画面'));
  });
  frame.srcdoc = html; document.body.appendChild(frame);
  try {
    await loaded; signal?.throwIfAborted();
    const doc = frame.contentDocument;
    if (!doc || !doc.querySelector('.frame')) throw new Error('无法读取相框画面');
    await doc.fonts.ready; await Promise.all(Array.from(doc.images, image => image.decode())); signal?.throwIfAborted();
    const bounds = Array.from(doc.querySelectorAll('svg.provider-logo, [data-dither]'), art => {
      const r = art.getBoundingClientRect();
      return { Left: Math.floor(r.left), Top: Math.floor(r.top), Right: Math.ceil(r.right), Bottom: Math.ceil(r.bottom) };
    });
    const documentXML = new XMLSerializer().serializeToString(doc.documentElement);
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${WIDTH}" height="${HEIGHT}"><foreignObject width="100%" height="100%">${documentXML}</foreignObject></svg>`;
    const image = new Image(); image.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
    await image.decode(); signal?.throwIfAborted();
    const canvas = document.createElement('canvas'); canvas.width = WIDTH; canvas.height = HEIGHT;
    const context = canvas.getContext('2d', { willReadFrequently: true });
    if (!context) throw new Error('浏览器无法生成相框图片');
    context.drawImage(image, 0, 0);
    const blob = await new Promise<Blob>((resolve, reject) => canvas.toBlob(value => value ? resolve(value) : reject(new Error('图片编码失败')), 'image/png'));
    signal?.throwIfAborted();
    return { png: new Uint8Array(await blob.arrayBuffer()), bounds };
  } finally { frame.remove(); }
}

export async function renderSnapshot(snapshot: FrameSnapshot, options: FrameOptions = {}, signal?: AbortSignal): Promise<Uint8Array> {
  signal?.throwIfAborted(); const html = await frameHTML(snapshot, options); signal?.throwIfAborted();
  const raster = await rasterFrameHTML(html, signal);
  const output = await quantizeFrame(raster.png, raster.bounds); signal?.throwIfAborted();
  return output;
}
