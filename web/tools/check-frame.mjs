import { build } from 'esbuild';
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildFrame } from '../build-frame.mjs';
import { chromePath } from './chrome.mjs';

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
await buildFrame();
for (const [entry, name, output] of [
  ['src/render.ts', 'FrameRasterTesting', 'dist/raster-test.js'],
  ['src/frame-bridge.ts', 'SharedFrameTest', 'dist/bridge-test.js'],
]) {
  await build({ absWorkingDir: web, entryPoints: [entry], bundle: true, format: 'iife', globalName: name, outfile: output });
}
const env = { ...process.env, PHOTOFRAME_WEB_PARITY: '1', CHROME_BIN: chromePath() };
execFileSync('go', ['test', './internal/dashboard', './internal/webframe', '-run', 'TestBrowser', '-v', '-count=1'], {
  cwd: path.dirname(web), env, stdio: 'inherit',
});
