import { build } from 'esbuild';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { Script } from 'node:vm';
import { buildFrame } from './build-frame.mjs';
await buildFrame();
const result = await build({ entryPoints: ['src/main.ts'], bundle: true, write: false, format: 'iife', target: ['chrome120'], minify: true, legalComments: 'none' });
const script = result.outputFiles[0].text.replaceAll('</script', '<\\/script');
new Script(script, { filename: 'photopainter-panel.js' });
// A callback is essential: minified JS may contain $`, $& or $' sequences,
// which String.replace would otherwise interpret as replacement directives.
const html = (await readFile('index.html', 'utf8')).replace('<!-- SCRIPT -->', () => `<script>${script}</script>`);
await mkdir('dist', { recursive: true });
await writeFile('dist/panel.html', html);
console.log(`Built dist/panel.html (${Buffer.byteLength(html)} bytes)`);
