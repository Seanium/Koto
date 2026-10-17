#!/usr/bin/env node
// Generate the Beta's Apple speech-balloon icon on macOS. No font is copied.
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
if (process.platform !== 'darwin') throw new Error('Regenerate Apple emoji artwork on macOS.');
const app = path.resolve(__dirname, '..');
const cache = path.join(app, '.cache/icon-generation');
fs.mkdirSync(cache, { recursive: true });
const env = { ...process.env, SWIFT_MODULECACHE_PATH: cache, CLANG_MODULE_CACHE_PATH: cache };
for (const [file, size] of [['appicon.png', 1024], ['trayicon.png', 36]]) {
  execFileSync('/usr/bin/swift', [path.join(__dirname, 'render-app-emoji.swift'), path.join(app, 'brand', file), String(size)], { env, stdio: 'inherit' });
}
const wails = process.env.KOTO_WAILS || path.join(app, '.tools/bin/wails3');
execFileSync(wails, ['generate', 'icons', '-input', 'brand/appicon.png', '-macfilename', 'build/darwin/icons.icns', '-windowsfilename', 'build/windows/icon.ico', '-sizes', '256,128,64,48,40,32,24,20,16'], { cwd: app, stdio: 'inherit' });
console.log('Generated Apple 💬 app, tray, ICNS and ICO icons.');
