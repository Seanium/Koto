import { test } from 'node:test';
import assert from 'node:assert/strict';
import { detectSystem } from './detection.mjs';
import { releaseAssets, downloadURL, matchingAsset } from './publication.mjs';

test('App and CLI resolve to different downloads for each native target', async () => {
  const config = { published: true, version: '0.1.0-beta.2', repository: 'https://github.com/Seanium/koto' };
  for (const [platform, browserName] of [['macos', 'macOS'], ['windows', 'Windows'], ['linux', 'Linux']]) {
    for (const arch of ['arm64', 'amd64']) {
      const system = await detectSystem({ userAgentData: { platform: browserName, mobile: false, getHighEntropyValues: async () => ({ architecture: arch === 'arm64' ? 'arm' : 'x86', bitness: '64' }) } });
      assert.equal(system.platform, platform);
      assert.equal(system.arch, arch);
      const app = downloadURL(matchingAsset(system, 'app'), config);
      const cli = downloadURL(matchingAsset(system, 'cli'), config);
      assert(app && cli);
      assert.notEqual(app, cli);
      assert.match(cli, /\/koto-cli-/);
      if (platform === 'windows') {
        assert.match(app, /\.exe$/);
        assert.doesNotMatch(app, /setup|installer/);
      }
    }
  }
  assert.equal(new Set(releaseAssets.map(asset => asset.file)).size, releaseAssets.length);
  assert.equal(downloadURL(releaseAssets[0].id, { ...config, published: false }), null);
});

test('Reduced user agents do not guess CPU architecture or select a mobile download', async () => {
  for (const browser of [
    { platform: 'MacIntel', userAgent: 'Intel Mac OS X' },
    { platform: 'Win32', userAgent: 'Windows NT 10.0; Win64; x64' },
    { platform: 'Linux x86_64' },
    { platform: 'MacIntel', maxTouchPoints: 5 },
  ]) {
    const system = await detectSystem(browser);
    assert.equal(matchingAsset(system, 'app'), null);
    assert.equal(matchingAsset(system, 'cli'), null);
  }
});
