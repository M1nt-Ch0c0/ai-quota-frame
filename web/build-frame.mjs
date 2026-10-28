import { execFileSync } from 'node:child_process';
import { mkdir, readFile, writeFile, copyFile } from 'node:fs/promises';
import { gzipSync } from 'node:zlib';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

export async function buildFrame() {
  const webRoot = path.dirname(fileURLToPath(import.meta.url));
  const repo = path.dirname(webRoot), dist = path.join(webRoot, 'dist');
  await mkdir(dist, { recursive: true });
  execFileSync('go', ['build', '-buildvcs=false', '-trimpath', '-ldflags=-s -w', '-o', path.join(dist, 'frame.wasm'), './cmd/web-frame'], {
    cwd: repo, stdio: 'inherit', env: { ...process.env, GOOS: 'js', GOARCH: 'wasm', CGO_ENABLED: '0' },
  });
  const wasm = await readFile(path.join(dist, 'frame.wasm'));
  await writeFile(path.join(dist, 'frame.wasm.gz'), gzipSync(wasm, { level: 9 }));
  const root = execFileSync('go', ['env', 'GOROOT'], { encoding: 'utf8' }).trim();
  await copyFile(path.join(root, 'lib', 'wasm', 'wasm_exec.js'), path.join(dist, 'wasm_exec.js'));
  const artifacts = {};
  for (const name of ['frame.wasm', 'frame.wasm.gz', 'wasm_exec.js']) {
    const data = await readFile(path.join(dist, name));
    artifacts[name] = { bytes: data.byteLength, sha256: createHash('sha256').update(data).digest('hex') };
  }
  await writeFile(path.join(dist, 'frame-manifest.json'), JSON.stringify({
    schema_version: 1, go_version: execFileSync('go', ['version'], { encoding: 'utf8' }).trim(), artifacts,
  }, null, 2) + '\n');
  console.log(`Built shared Go frame engine (${wasm.byteLength} bytes)`);
}
