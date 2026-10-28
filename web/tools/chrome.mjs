import { statSync } from 'node:fs';
import path from 'node:path';

const isFile = candidate => {
  try { return statSync(candidate).isFile(); } catch { return false; }
};

export function chromePath() {
  if (process.env.CHROME_BIN) {
    if (!isFile(process.env.CHROME_BIN)) throw new Error('CHROME_BIN does not identify a Chrome/Chromium executable');
    return process.env.CHROME_BIN;
  }
  const candidates = ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'];
  for (const variable of ['PROGRAMFILES', 'PROGRAMFILES(X86)', 'LOCALAPPDATA']) {
    if (process.env[variable]) candidates.push(path.join(process.env[variable], 'Google/Chrome/Application/chrome.exe'));
  }
  for (const directory of (process.env.PATH || '').split(path.delimiter)) {
    for (const executable of ['google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser', 'chrome', 'chrome.exe']) {
      candidates.push(path.join(directory, executable));
    }
  }
  const candidate = candidates.find(isFile);
  if (!candidate) throw new Error('Set CHROME_BIN to a Chrome/Chromium executable; browser checks must not silently skip');
  return candidate;
}
