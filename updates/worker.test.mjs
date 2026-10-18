import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createWorker, compare, parseSums, parseAtom } from './worker.mjs';

const github = 'https://github.com/Seanium/koto';
const api = 'https://api.github.com/repos/Seanium/koto';
const origin = 'https://updates.kotoai.site';
const name = 'koto-cli-linux-amd64';
const payload = 'native binary fixture';
const hash = createHash('sha256').update(payload).digest('hex');
const sums = hash + '  ' + name + '\n';
function release(tag, extra = {}) {
  return { tag_name: tag, body: 'Release notes', assets: [{ name, size: payload.length, browser_download_url: github + '/releases/download/' + tag + '/' + name, digest: 'sha256:' + hash }], ...extra };
}
function fixture(list = [release('v0.1.0')]) {
  const entries = new Map(), calls = [], tasks = [];
  let mode = 'api', sumText = sums;
  const cache = { async match(key) { return entries.get(key)?.clone(); }, async put(key, response) { entries.set(key, response.clone()); } };
  const worker = createWorker({ cache: () => cache, fetcher: async (url, options) => {
    calls.push({ url, options });
    if (mode === 'offline') throw new Error('offline');
    if (url.startsWith(api)) {
      if (mode === 'fallback') return new Response('limited', { status: 403 });
      if (url === api + '/releases?per_page=100') return Response.json(list);
      const tag = url.slice((api + '/releases/tags/').length);
      const r = list.find(r => r.tag_name === tag);
      return r ? Response.json(r) : new Response('', { status: 404 });
    }
    if (url.endsWith('/SHA256SUMS')) return new Response(sumText);
    if (url === github + '/releases/latest') {
      const stable = list.find(r => !r.prerelease && !r.tag_name.includes('-'));
      return new Response(null, { status: stable ? 302 : 404, headers: stable ? { Location: github + '/releases/tag/' + stable.tag_name } : {} });
    }
    if (url === github + '/releases.atom') return new Response('<feed>' + list.filter(r => !r.draft).map(r => '<entry><link href="' + github + '/releases/tag/' + r.tag_name + '"/><content>notes</content></entry>').join('') + '</feed>');
    throw new Error('Unexpected URL: ' + url);
  }});
  const ctx = { waitUntil(promise) { tasks.push(promise); } };
  return {
    entries, calls,
    set mode(value) { mode = value; },
    set sums(value) { sumText = value; },
    async call(path = '/api/releases', env = {}, method = 'GET') {
      const result = await worker.fetch(new Request(origin + path, { method }), env, ctx);
      await Promise.all(tasks.splice(0));
      return result;
    },
    expire() { for (const key of entries.keys()) if (!key.endsWith('-good')) entries.delete(key); }
  };
}

test('semantic order preserves numeric prerelease ordering and stable precedence', () => {
  assert.equal(compare('v0.1.0-beta.10', 'v0.1.0-beta.9'), 1);
  assert.equal(compare('v0.1.0', 'v0.1.0-beta.99'), 1);
  assert.equal(compare('v0.1.1', 'v0.1.0'), 1);
  assert.equal(compare('v0.1.0-a', 'v0.1.0-9'), 1);
  assert.equal(compare('v0.1.0-a.1', 'v0.1.0-a'), 1);
});
test('publishes only newest stable and overall, with exact trusted digests', async () => {
  const f = fixture([release('v0.0.9'), release('v9.0.0', { draft: true }), release('v0.1.0'), release('v0.2.0-beta.9', { prerelease: true }), release('v0.2.0-beta.10', { prerelease: true })]);
  const response = await f.call();
  const list = await response.json();
  assert.deepEqual(list.map(r => r.tag_name), ['v0.1.0', 'v0.2.0-beta.10']);
  assert.equal(response.headers.get('Cache-Control'), 'public, max-age=300');
  assert.equal(list[0].assets[0].digest, 'sha256:' + hash);
  assert.equal(list[0].assets[1].size, Buffer.byteLength(sums));
  assert.equal(list[0].assets[1].digest, 'sha256:' + createHash('sha256').update(sums).digest('hex'));
  const count = f.calls.length;
  await f.call();
  assert.equal(f.calls.length, count, 'cache avoids another upstream request');
});
test('latest defaults stable and beta is explicit; drafts are never returned', async () => {
  const f = fixture([release('v0.1.0'), release('v0.2.0-beta.1', { prerelease: true })]);
  assert.equal((await (await f.call('/api/latest')).json()).tag_name, 'v0.1.0');
  assert.equal((await (await f.call('/api/latest?channel=beta')).json()).tag_name, 'v0.2.0-beta.1');
  assert.equal(await (await f.call('/api/latest?format=tag')).text(), 'v0.1.0\n');
});
test('server secret is sent only to GitHub API and never returned', async () => {
  const f = fixture();
  const response = await f.call('/api/releases', { GITHUB_TOKEN: 'test-secret-only' });
  assert.ok(!JSON.stringify(await response.json()).includes('test-secret-only'));
  for (const call of f.calls) assert.equal(call.options.headers.Authorization, call.url.startsWith(api) ? 'Bearer test-secret-only' : undefined);
  assert.equal(f.calls[0].options.redirect, 'error', 'authenticated fetch must not redirect credentials');
});
test('API refusal falls back to public redirects, Atom and checksums', async () => {
  const f = fixture([release('v0.1.0'), release('v0.2.0-beta.10', { prerelease: true })]);
  f.mode = 'fallback';
  const response = await f.call();
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('Cache-Control'), 'public, max-age=60');
  const list = await response.json();
  assert.deepEqual(list.map(r => r.tag_name), ['v0.1.0', 'v0.2.0-beta.10']);
  assert.equal(list[0].assets[0].size, 0, 'unknown binary size must not be invented');
  assert.equal(list[0].assets[0].digest, 'sha256:' + hash);
  assert.equal(list[0].stale, false, 'current public release metadata is not stale');
});
test('Atom plain semver marked prerelease on GitHub never enters the stable channel', async () => {
  const f = fixture([release('v0.2.0', { prerelease: true }), release('v0.1.0')]);
  f.mode = 'fallback';
  assert.equal((await (await f.call('/api/latest')).json()).tag_name, 'v0.1.0');
  const beta = await (await f.call('/api/latest?channel=beta')).json();
  assert.equal(beta.tag_name, 'v0.2.0');
  assert.equal(beta.prerelease, true);
});
test('Atom alone cannot confirm a stable release', async () => {
  const f = fixture([release('v0.2.0', { prerelease: true })]);
  f.mode = 'fallback';
  assert.equal((await f.call('/api/latest')).status, 503);
  assert.equal((await f.call('/api/latest?format=tag')).status, 503);
  assert.equal((await (await f.call('/api/latest?channel=beta')).json()).tag_name, 'v0.2.0');
});
test('Atom cannot freshly confirm the stable status of a previously known release', async () => {
  const list = [release('v0.2.0')];
  const f = fixture(list);
  await f.call(); f.expire();
  list[0].prerelease = true; f.mode = 'fallback';
  const got = await (await f.call('/api/latest')).json();
  assert.equal(got.tag_name, 'v0.2.0');
  assert.equal(got.stale, true);
  assert.equal((await f.call('/api/latest?format=tag')).status, 503);
});
test('complete outage serves marked last-good metadata, never unmarked current', async () => {
  const f = fixture();
  await f.call(); f.expire(); f.mode = 'offline';
  const response = await f.call();
  assert.equal(response.status, 200);
  assert.equal((await response.json())[0].stale, true);
  assert.equal((await f.call('/api/latest?format=tag')).status, 503);
});
test('outage without cache is retryable and never caches an error', async () => {
  const f = fixture(); f.mode = 'offline';
  const response = await f.call();
  assert.equal(response.status, 503);
  assert.equal(response.headers.get('Retry-After'), '60');
  assert.equal(response.headers.get('Cache-Control'), 'no-store');
  assert.equal(f.entries.size, 0);
  f.mode = 'api';
  assert.equal((await f.call()).status, 200);
});
test('exact version lookup never substitutes latest and supports older rollback', async () => {
  const f = fixture([release('v0.1.0'), release('v0.0.9')]);
  const r = await (await f.call('/api/releases/tags/v0.0.9')).json();
  assert.equal(r.tag_name, 'v0.0.9');
  assert.ok(r.assets.every(a => a.browser_download_url.includes('/v0.0.9/')));
  assert.equal((await f.call('/api/releases/tags/v0.0.09')).status, 400);
});
test('parallel cold requests share one upstream lookup', async () => {
  const f = fixture();
  const responses = await Promise.all(Array.from({ length: 12 }, () => f.call()));
  assert.ok(responses.every(r => r.status === 200));
  assert.equal(f.calls.filter(c => c.url.startsWith(api)).length, 1);
});
test('download routes only redirect to a verified release asset', async () => {
  const f = fixture();
  const response = await f.call('/download/' + name);
  assert.equal(response.status, 302);
  assert.equal(response.headers.get('Location'), github + '/releases/download/v0.1.0/' + name);
  assert.equal((await f.call('/download/unknown.exe')).status, 404);
  assert.equal((await f.call('/download/https://untrusted.invalid')).status, 404);
});
test('unsupported routes, methods, channels and formats fail without upstream work', async () => {
  const f = fixture();
  for (const [path, method, status] of [['/', 'GET', 404], ['/api/releases', 'POST', 405], ['/api/latest?channel=nightly', 'GET', 400], ['/api/releases?format=tag', 'GET', 400]]) assert.equal((await f.call(path, {}, method)).status, status);
  assert.equal(f.calls.length, 0);
});
test('HEAD has no body but same status and cache metadata', async () => {
  const f = fixture();
  const response = await f.call('/api/releases', {}, 'HEAD');
  assert.equal(response.status, 200);
  assert.equal(await response.text(), '');
  assert.equal(response.headers.get('Cache-Control'), 'public, max-age=300');
});
test('malformed and duplicate checksums cannot become update candidates', async () => {
  for (const text of ['bad checksum', sums + sums, '0'.repeat(64) + '  ../escape\n']) {
    assert.throws(() => parseSums(text));
    const f = fixture(); f.sums = text;
    assert.equal((await f.call()).status, 503);
    assert.equal(f.entries.size, 0);
  }
});
test('untrusted Atom URLs, malformed versions and numeric leading zeros are ignored', () => {
  const xml = ['https://evil.invalid/releases/tag/v0.1.0', github + '/releases/tag/v01.1.0', github + '/releases/tag/v0.1.0-beta.01', github + '/releases/tag/v0.1.0'].map(url => '<entry><link href="' + url + '"/><content>x &amp; y</content></entry>').join('');
  assert.deepEqual(parseAtom(xml).map(r => [r.tag_name, r.body]), [['v0.1.0', 'x & y']]);
});
test('large upstream response is bounded and can use public fallback', async () => {
  const f = fixture([release('v0.1.0', { body: 'x'.repeat(3 << 20) })]);
  const response = await f.call();
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('Cache-Control'), 'public, max-age=60');
});

test('older public pages cannot replace a newer known release during API failure', async () => {
  const list = [release('v0.1.1'), release('v0.1.0')];
  const f = fixture(list);
  await f.call(); f.expire();
  list.shift(); f.mode = 'fallback';
  const response = await f.call();
  const got = await response.json();
  assert.equal(got[0].tag_name, 'v0.1.1');
  assert.equal(got[0].stale, true);
  assert.equal((await f.call('/api/latest?format=tag')).status, 503);
});

test('a newer public release supersedes an older good cache during API failure', async () => {
  const list = [release('v0.1.0')];
  const f = fixture(list);
  await f.call(); f.expire();
  list.unshift(release('v0.1.1')); f.mode = 'fallback';
  const got = await (await f.call()).json();
  assert.equal(got[0].tag_name, 'v0.1.1');
  assert.equal(got[0].stale, false);
});
