#!/usr/bin/env node
// Derive every icon from the checked-in Apple artwork; never render a system font.
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const app = path.resolve(__dirname, '..');
const sharp = require(process.env.KOTO_ICON_SHARP_PATH || path.join(app, '.tools/icon-generation/node_modules/sharp'));
const source = path.join(app, 'brand/appicon.png');
const wails = process.env.KOTO_WAILS || path.join(app, '.tools/bin', process.platform === 'win32' ? 'wails3.exe' : 'wails3');

(async () => {
  for (const [file, size] of [['brand/trayicon.png', 64], ['../site/assets/koto.png', 128], ['../site/favicon.png', 64]]) {
    await sharp(source).resize(size, size).png().toFile(path.join(app, file));
  }
  execFileSync(wails, ['generate', 'icons', '-input', 'brand/appicon.png', '-macfilename', 'build/darwin/icons.icns', '-windowsfilename', 'build/windows/icon.ico', '-sizes', '256,128,64,48,40,32,24,20,16'], { cwd: app, stdio: 'inherit' });
  console.log('Generated fixed Apple app, tray, website, ICNS and ICO icons.');
})().catch(error => { console.error(error); process.exitCode = 1; });
