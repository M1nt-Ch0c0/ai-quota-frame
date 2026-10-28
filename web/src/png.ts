import { concat, equal, utf8, view } from './bytes.ts';
export const WIDTH = 800, HEIGHT = 480, MAX_PNG = 5 * 1024 * 1024;
export const PALETTE = [[0,0,0],[255,255,255],[255,255,0],[255,0,0],[0,0,255],[0,255,0]] as const;
const SIGNATURE = new Uint8Array([137,80,78,71,13,10,26,10]);
const colors = new Set(PALETTE.map(c => c.join(',')));
export function crc32(bytes: Uint8Array): number {
  let crc = 0xffffffff;
  for (const value of bytes) { crc ^= value; for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0); }
  return (crc ^ 0xffffffff) >>> 0;
}
function big32(value: number): Uint8Array { const bytes = new Uint8Array(4); view(bytes).setUint32(0, value); return bytes; }
export function pngChunk(type: string, data: Uint8Array): Uint8Array { const body = concat(utf8(type), data); return concat(big32(data.length), body, big32(crc32(body))); }
async function transform(data: Uint8Array, compress: boolean, max = Infinity): Promise<Uint8Array> {
  const stream = compress ? new CompressionStream('deflate') : new DecompressionStream('deflate');
  const reader = new Blob([data as BlobPart]).stream().pipeThrough(stream).getReader();
  const pieces: Uint8Array[] = []; let size = 0;
  for (;;) {
    const result = await reader.read(); if (result.done) break;
    size += result.value.length;
    if (size > max) { await reader.cancel(); throw new Error('PNG 解压大小超限'); }
    pieces.push(result.value);
  }
  return concat(...pieces);
}
export async function encodePNG(indices: Uint8Array): Promise<Uint8Array> {
  if (indices.length !== WIDTH * HEIGHT || indices.some(i => i >= PALETTE.length)) throw new Error('图片像素不符合六色格式');
  const rows = new Uint8Array(HEIGHT * (WIDTH + 1));
  for (let y = 0; y < HEIGHT; y++) rows.set(indices.subarray(y * WIDTH, (y + 1) * WIDTH), y * (WIDTH + 1) + 1);
  const header = concat(big32(WIDTH), big32(HEIGHT), new Uint8Array([8,3,0,0,0]));
  return concat(SIGNATURE, pngChunk('IHDR', header), pngChunk('PLTE', new Uint8Array(PALETTE.flat())), pngChunk('IDAT', await transform(rows, true)), pngChunk('IEND', new Uint8Array()));
}
export async function validatePNG(png: Uint8Array): Promise<void> {
  if (png.length > MAX_PNG || png.length < 57 || !equal(png.subarray(0,8), SIGNATURE)) throw new Error('请选择不超过 5 MiB 的 PNG');
  let offset = 8, type = -1, bpp = 0, ended = false, idatEnded = false, sawIDAT = false;
  let palette: Uint8Array | undefined; const compressed: Uint8Array[] = [];
  while (offset < png.length) {
    if (png.length - offset < 12) throw new Error('PNG 数据不完整');
    const size = view(png).getUint32(offset); if (size > png.length - offset - 12) throw new Error('PNG 区块长度错误');
    const name = new TextDecoder().decode(png.subarray(offset + 4, offset + 8));
    if (!/^[A-Za-z]{2}[A-Z][A-Za-z]$/.test(name)) throw new Error('PNG 区块名称无效');
    const data = png.subarray(offset + 8, offset + 8 + size);
    if (crc32(png.subarray(offset + 4, offset + 8 + size)) !== view(png).getUint32(offset + 8 + size)) throw new Error('PNG CRC 校验失败');
    if (offset === 8 && name !== 'IHDR') throw new Error('PNG 缺少文件头');
    if (name === 'IHDR') {
      if (offset !== 8 || size !== 13 || view(data).getUint32(0) !== WIDTH || view(data).getUint32(4) !== HEIGHT || data[8] !== 8 || ![2,3,6].includes(data[9]) || data[10] || data[11] || data[12]) throw new Error('图片须为 800×480、8 位、非交错 PNG');
      type = data[9]; bpp = type === 2 ? 3 : type === 6 ? 4 : 1;
    } else if (name === 'PLTE') {
      if (palette || sawIDAT || !size || size % 3 || size > 768) throw new Error('PNG 调色板无效');
      palette = data;
    } else if (name === 'IDAT') {
      if (idatEnded || (type === 3 && !palette)) throw new Error('PNG 图片数据顺序错误');
      compressed.push(data); sawIDAT = true;
    } else if (name === 'IEND') {
      if (size || !sawIDAT || offset + 12 !== png.length) throw new Error('PNG 尾部数据无效');
      ended = true;
    } else {
      if (sawIDAT) idatEnded = true;
      if (name === 'tRNS' || name === 'acTL' || (name.charCodeAt(0) & 32) === 0) throw new Error('PNG 不支持透明、动画或未知关键区块');
    }
    offset += size + 12;
  }
  if (!ended) throw new Error('PNG 缺少结束标记');
  const rowSize = WIDTH * bpp, expected = HEIGHT * (rowSize + 1);
  const raw = await transform(concat(...compressed), false, expected);
  if (raw.length !== expected) throw new Error('PNG 解压长度不匹配');
  let previous = new Uint8Array(rowSize);
  const paeth = (a:number,b:number,c:number) => { const p=a+b-c, pa=Math.abs(p-a),pb=Math.abs(p-b),pc=Math.abs(p-c); return pa<=pb&&pa<=pc?a:pb<=pc?b:c; };
  for (let y = 0; y < HEIGHT; y++) {
    const filter = raw[y * (rowSize + 1)]; if (filter > 4) throw new Error('PNG 滤波类型错误');
    const row = raw.slice(y * (rowSize + 1) + 1, (y + 1) * (rowSize + 1));
    for (let x = 0; x < rowSize; x++) {
      const left = x >= bpp ? row[x-bpp] : 0, above = previous[x], upperLeft = x >= bpp ? previous[x-bpp] : 0;
      row[x] = (row[x] + (filter===1?left:filter===2?above:filter===3?Math.floor((left+above)/2):filter===4?paeth(left,above,upperLeft):0)) & 255;
    }
    for (let x = 0; x < rowSize; x += bpp) {
      const rgb = type===3 ? palette?.subarray(row[x]*3,row[x]*3+3) : row.subarray(x,x+3);
      if (!rgb || rgb.length !== 3 || !colors.has(Array.from(rgb).join(',')) || (type===6 && row[x+3]!==255)) throw new Error('PNG 像素必须是不透明的黑、白、黄、红、蓝或绿');
    }
    previous = row;
  }
}
export function quantize(rgba: Uint8ClampedArray): Uint8Array {
  if (rgba.length !== WIDTH * HEIGHT * 4) throw new Error('图片尺寸错误');
  const indices = new Uint8Array(WIDTH * HEIGHT);
  for (let i = 0; i < indices.length; i++) {
    const alpha = rgba[i*4+3] / 255; let nearest = 0, distance = Infinity;
    for (let c = 0; c < PALETTE.length; c++) {
      let squared = 0;
      for (let channel = 0; channel < 3; channel++) { const value = rgba[i*4+channel] * alpha + 255*(1-alpha); squared += (value - PALETTE[c][channel]) ** 2; }
      if (squared < distance) { nearest=c; distance=squared; }
    }
    indices[i] = nearest;
  }
  return indices;
}
