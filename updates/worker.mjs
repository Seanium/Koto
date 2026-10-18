// Update metadata only. Release binaries remain on GitHub.
const REPO = 'Seanium/koto';
const GITHUB = 'https://github.com/' + REPO;
const API = 'https://api.github.com/repos/' + REPO;
const ORIGIN = 'https://updates.kotoai.site';
const TTL = 300, BRIEF = 60, KEEP = 30 * 86400;
const TAG = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;
const FILE = /^koto-(?:cli-)?(?:darwin|linux|windows)-(?:arm64|amd64)(?:\.dmg|\.exe|\.deb)?$/;

export function compare(a, b) {
  const x = TAG.exec(a), y = TAG.exec(b);
  if (!x || !y) throw new Error('Invalid version');
  for (let i = 1; i <= 3; i++) {
    if (x[i].length !== y[i].length) return x[i].length > y[i].length ? 1 : -1;
    if (x[i] !== y[i]) return x[i] > y[i] ? 1 : -1;
  }
  if (x[4] === y[4]) return 0;
  if (!x[4]) return 1;
  if (!y[4]) return -1;
  const p = x[4].split('.'), q = y[4].split('.');
  for (let i = 0; i < Math.min(p.length, q.length); i++) {
    if (p[i] === q[i]) continue;
    const pn = /^\d+$/.test(p[i]), qn = /^\d+$/.test(q[i]);
    if (pn !== qn) return pn ? -1 : 1;
    if (pn && p[i].length !== q[i].length) return p[i].length > q[i].length ? 1 : -1;
    return p[i] > q[i] ? 1 : -1;
  }
  return Math.sign(p.length - q.length);
}
function validTag(tag) {
  const m = typeof tag === 'string' && tag.length <= 255 && TAG.exec(tag);
  return !!m && m.slice(1, 4).every(n => BigInt(n) <= 18446744073709551615n) && !(m[4] || '').split('.').some(p => /^0\d+$/.test(p));
}
function newest(list, stable) {
  return list.filter(r => validTag(r.tag_name) && !r.draft && (!stable || (!r.prerelease && !TAG.exec(r.tag_name)[4])))
    .sort((a, b) => compare(b.tag_name, a.tag_name))[0];
}
export function parseSums(text) {
  const sums = new Map();
  for (const line of text.split(/\r?\n/)) {
    if (!line.trim()) continue;
    const match = /^([a-fA-F0-9]{64})\s+\*?(\S+)$/.exec(line);
    if (!match) throw new Error('Invalid release checksum');
    const [, hash, name] = match;
    if (!FILE.test(name)) continue;
    if (sums.has(name)) throw new Error('Duplicate release checksum');
    sums.set(name, hash.toLowerCase());
  }
  if (!sums.size) throw new Error('No release checksums');
  return sums;
}
function unxml(value) {
  return value.replace(/&quot;/g, '"').replace(/&apos;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
}
export function parseAtom(xml) {
  const result = [];
  for (const [, entry] of xml.matchAll(/<entry>([\s\S]*?)<\/entry>/g)) {
    const href = /<link[^>]*href="([^"]+)"/.exec(entry)?.[1];
    if (!href || !href.startsWith(GITHUB + '/releases/tag/')) continue;
    const tag = href.slice((GITHUB + '/releases/tag/').length);
    if (!validTag(tag)) continue;
    // Atom does not expose GitHub's prerelease flag. Even a plain semver tag
    // may be marked prerelease, so only /releases/latest can confirm stable.
    result.push({ tag_name: tag, prerelease: true,
      body: unxml(/<content[^>]*>([\s\S]*?)<\/content>/.exec(entry)?.[1] || ''),
      published_at: /<updated>([^<]+)<\/updated>/.exec(entry)?.[1] || null });
  }
  return result;
}
function json(value, status = 200, ttl = 0) {
  return new Response(JSON.stringify(value), { status, headers: {
    'Content-Type': 'application/json; charset=utf-8',
    'Cache-Control': ttl ? 'public, max-age=' + ttl : 'no-store',
    'X-Content-Type-Options': 'nosniff',
    ...(status === 503 ? { 'Retry-After': String(BRIEF) } : {})
  }});
}
async function boundedText(response, limit) {
  if (!response.ok) { await response.body?.cancel(); throw new Error('Upstream HTTP ' + response.status); }
  if (Number(response.headers.get('Content-Length')) > limit) { await response.body?.cancel(); throw new Error('Upstream response too large'); }
  const reader = response.body.getReader(), chunks = [];
  let length = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      length += value.length;
      if (length > limit) { await reader.cancel(); throw new Error('Upstream response too large'); }
      chunks.push(value);
    }
  } finally { reader.releaseLock(); }
  const all = new Uint8Array(length);
  let offset = 0;
  for (const chunk of chunks) { all.set(chunk, offset); offset += chunk.length; }
  return new TextDecoder().decode(all);
}
async function sha256(bytes) {
  const hash = new Uint8Array(await crypto.subtle.digest('SHA-256', bytes));
  return Array.from(hash, b => b.toString(16).padStart(2, '0')).join('');
}

// Dependencies are explicit so failure and cache paths run against HTTP-shaped
// responses in tests without a Cloudflare account or GitHub credentials.
export function createWorker({ fetcher = fetch, cache = () => caches.default } = {}) {
  const inflight = new Map();
  async function request(url, env, api = false, redirect = 'follow') {
    const headers = { 'User-Agent': 'koto-updates', Accept: api ? 'application/vnd.github+json' : '*/*' };
    if (api && env.GITHUB_TOKEN) headers.Authorization = 'Bearer ' + env.GITHUB_TOKEN;
    return fetcher(url, { headers, redirect, signal: AbortSignal.timeout(8000) });
  }
  async function apiJSON(path, env) {
    return JSON.parse(await boundedText(await request(API + path, env, true, 'error'), 2 << 20));
  }
  async function normalize(r, env) {
    if (!validTag(r.tag_name) || r.draft) throw new Error('Not a published release');
    const base = GITHUB + '/releases/download/' + r.tag_name + '/';
    const text = await boundedText(await request(base + 'SHA256SUMS', env), 128 << 10);
    const sums = parseSums(text), bytes = new TextEncoder().encode(text);
    const assets = [];
    for (const [name, hash] of sums) {
      const originals = (r.assets || []).filter(a => a.name === name);
      if (r.assets && originals.length !== 1) throw new Error('Release asset missing or duplicated');
      const original = originals[0];
      if (original && (original.browser_download_url !== base + name || !Number.isSafeInteger(original.size) || original.size <= 0 || original.size > 1 << 30 || (original.digest && original.digest !== 'sha256:' + hash))) throw new Error('Invalid release asset');
      assets.push({ name, browser_download_url: base + name, size: original?.size || 0, digest: 'sha256:' + hash });
    }
    assets.push({ name: 'SHA256SUMS', browser_download_url: base + 'SHA256SUMS', size: bytes.length, digest: 'sha256:' + await sha256(bytes) });
    return { tag_name: r.tag_name, draft: false, prerelease: !!r.prerelease || !!TAG.exec(r.tag_name)[4],
      body: typeof r.body === 'string' ? r.body : '', html_url: GITHUB + '/releases/tag/' + r.tag_name,
      published_at: r.published_at || null, assets, stale: false };
  }
  async function readCache(key) {
    try { const response = await cache().match(ORIGIN + '/__feed-v1/' + key); return response ? await response.json() : null; }
    catch { return null; }
  }
  function remember(ctx, key, value, ttl) {
    ctx.waitUntil(cache().put(ORIGIN + '/__feed-v1/' + key, json(value, 200, ttl)).catch(() => {}));
  }
  async function fromPages(env, tag) {
    if (tag) return [await normalize({ tag_name: tag }, env)];
    const [redirect, atom] = await Promise.allSettled([
      request(GITHUB + '/releases/latest', env, false, 'manual'),
      request(GITHUB + '/releases.atom', env).then(r => boundedText(r, 2 << 20)).then(parseAtom)
    ]);
    const list = atom.status === 'fulfilled' ? atom.value : [];
    if (redirect.status === 'fulfilled') {
      const location = redirect.value.headers.get('Location') || '';
      const prefix = GITHUB + '/releases/tag/';
      const latest = location.startsWith(prefix) ? location.slice(prefix.length) : '';
      if ([301, 302, 303, 307, 308].includes(redirect.value.status) && validTag(latest) && !TAG.exec(latest)[4]) {
        const existing = list.find(r => r.tag_name === latest);
        if (existing) existing.prerelease = false;
        else list.push({ tag_name: latest, prerelease: false });
      }
      await redirect.value.body?.cancel();
    }
    const selected = [newest(list, true), newest(list, false)].filter((r, i, all) => r && all.findIndex(x => x?.tag_name === r.tag_name) === i);
    if (!selected.length) throw new Error('No release metadata');
    return Promise.all(selected.map(r => normalize(r, env)));
  }
  async function load(ctx, env, tag) {
    const key = tag || 'releases';
    const hit = await readCache(key);
    if (hit) return hit;
    if (inflight.has(key)) return inflight.get(key);
    const pending = (async () => {
      let list, degraded = false;
      try {
        if (tag) {
          const found = await apiJSON('/releases/tags/' + tag, env);
          if (found.tag_name !== tag) throw new Error('Unexpected release tag');
          list = [await normalize(found, env)];
        } else {
          const found = await apiJSON('/releases?per_page=100', env);
          if (!Array.isArray(found)) throw new Error('Invalid release list');
          const selected = [newest(found, true), newest(found, false)].filter((r, i, all) => r && all.findIndex(x => x?.tag_name === r.tag_name) === i);
          if (!selected.length) throw new Error('No published release');
          list = await Promise.all(selected.map(r => normalize(r, env)));
        }
      } catch {
        degraded = true;
        try {
          list = await fromPages(env, tag);
          // GitHub's public pages may lag behind its API. Keep a newer known
          // release, marked stale, instead of silently advertising a downgrade.
          const good = await readCache(key + '-good');
          // An Atom entry cannot revoke or freshly confirm a stable status
          // learned from the API. Keep that known status explicitly stale.
          list = list.map(r => {
            const knownStable = (good?.list || []).find(p => p.tag_name === r.tag_name && !p.prerelease);
            return r.prerelease && !TAG.exec(r.tag_name)[4] && knownStable ? { ...knownStable, stale: true } : r;
          });
          const merged = [...list, ...(good?.list || []).filter(r => !list.some(p => p.tag_name === r.tag_name)).map(r => ({ ...r, stale: true }))];
          list = tag ? list : [newest(merged, true), newest(merged, false)].filter((r, i, all) => r && all.findIndex(x => x?.tag_name === r.tag_name) === i);
        }
        catch {
          const good = await readCache(key + '-good');
          if (!good?.list?.length) throw new Error('Update information is temporarily unavailable');
          list = good.list.map(r => ({ ...r, stale: true }));
        }
      }
      const value = { list, degraded };
      remember(ctx, key, value, degraded ? BRIEF : TTL);
      if (!list.some(r => r.stale)) remember(ctx, key + '-good', value, KEEP);
      return value;
    })();
    inflight.set(key, pending);
    try { return await pending; } finally { inflight.delete(key); }
  }
  return {
    async fetch(req, env = {}, ctx) {
      const url = new URL(req.url);
      if (!['GET', 'HEAD'].includes(req.method)) return new Response('Method not allowed', { status: 405, headers: { Allow: 'GET, HEAD' } });
      const tagMatch = /^\/api\/releases\/tags\/(v[^/]+)$/.exec(url.pathname);
      const route = tagMatch ? 'tag' : url.pathname === '/api/releases' ? 'list' : url.pathname === '/api/latest' ? 'latest' : url.pathname.startsWith('/download/') ? 'download' : '';
      if (!route) return json({ error: 'Not found' }, 404);
      if (tagMatch && !validTag(tagMatch[1])) return json({ error: 'Invalid version' }, 400);
      const channel = url.searchParams.get('channel') || 'stable';
      if (!['stable', 'beta'].includes(channel)) return json({ error: 'Invalid update channel' }, 400);
      const format = url.searchParams.get('format');
      if (format && (format !== 'tag' || route !== 'latest')) return json({ error: 'Invalid response format' }, 400);
      try {
        const got = await load(ctx, env, tagMatch?.[1]);
        const ttl = got.degraded ? BRIEF : TTL;
        const release = route === 'tag' ? got.list[0] : newest(got.list, channel === 'stable');
        let response;
        if (route === 'list') response = json(got.list, 200, ttl);
        else if (!release) response = json({ error: 'No published ' + channel + ' release' }, 503);
        else if (format === 'tag') {
          if (release.stale) return json({ error: 'Latest version cannot be confirmed' }, 503);
          response = new Response(release.tag_name + '\n', { headers: { 'Content-Type': 'text/plain; charset=utf-8', 'Cache-Control': 'public, max-age=' + ttl, 'X-Content-Type-Options': 'nosniff' } });
        } else if (route === 'download') {
          const name = url.pathname.slice('/download/'.length);
          const asset = release.assets.find(a => a.name === name);
          response = asset ? new Response(null, { status: 302, headers: { Location: asset.browser_download_url, 'Cache-Control': 'public, max-age=' + ttl } }) : json({ error: 'Asset not found' }, 404);
        } else response = json(release, 200, ttl);
        return req.method === 'HEAD' ? new Response(null, response) : response;
      } catch {
        return json({ error: 'Update information is temporarily unavailable' }, 503);
      }
    }
  };
}
export default createWorker();
