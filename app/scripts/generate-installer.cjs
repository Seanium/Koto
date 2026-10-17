#!/usr/bin/env node
// Regenerate Finder's standard/Retina backgrounds with the Mac's Apple emoji.
// Uses project-local sharp and Apple's installed font; no font file is copied.
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const app = path.resolve(__dirname, '..');
const sharp = require(process.env.KOTO_ICON_SHARP_PATH || path.join(app, '.tools/icon-generation/node_modules/sharp'));
const source = fs.readFileSync(path.join(app, 'brand/installer.svg'));
if (process.platform !== 'darwin') throw new Error('Generate Apple emoji installer artwork on macOS.');
const cache = path.join(app, '.cache/dmg-installer');
fs.mkdirSync(cache, { recursive: true });
(async () => {
  for (const scale of [1, 2]) {
    const base = path.join(cache, `background-base-${scale}.png`);
    await sharp(source, { density: 72 * scale }).resize(660 * scale, 410 * scale).png().toFile(base);
    execFileSync('/usr/bin/swift', [path.join(__dirname, 'render-installer-emoji.swift'), base,
      path.join(app, `brand/installer${scale === 2 ? '@2x' : ''}.png`), String(scale)], {
      stdio: 'inherit', env: { ...process.env, SWIFT_MODULECACHE_PATH: path.join(cache, 'clang'), CLANG_MODULE_CACHE_PATH: path.join(cache, 'clang') },
    });
    fs.unlinkSync(base);
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
