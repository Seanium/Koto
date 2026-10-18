import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, readFile, rm, readdir, symlink, realpath } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';

const installer = fileURLToPath(new URL('./install.sh', import.meta.url));
const version = '9.0.0-beta.1';
// The shell only verifies and dispatches. Atomic install/lock/recovery behavior
// is covered by the native Go tests and Windows installer acceptance suite.
const payload = `#!/bin/sh
case "$1" in
 version) printf 'koto ${version}\\n' ;;
 --koto-install)
  printf '%s\\n%s\\n' "$2" "$3" > "$MOCK_ROOT/dispatch"
  [ "$SCENARIO" != install-failure ] || exit 42
  cp "$0" "$2" ;;
 *) exit 99 ;;
esac
`;
const digest = createHash('sha256').update(payload).digest('hex');

test('CLI shell installer validates before replacing and cleans failed downloads', { skip: process.platform === 'win32' }, async t => {
  for (const scenario of ['ok', 'corrupt', 'missing-checksum', 'duplicate-checksum', 'download-failure', 'wrong-version', 'symlink', 'install-failure']) {
    await t.test(scenario, async () => {
      const root = await mkdtemp(join(tmpdir(), 'koto-install-test-'));
      try {
        const bin = join(root, 'tools');
        const dest = join(root, 'with spaces');
        await mkdir(bin); await mkdir(dest);
        await writeFile(join(root, 'payload'), scenario === 'wrong-version' ? '#!/bin/sh\necho koto 1.0.0\n' : payload);
        const hash = scenario === 'wrong-version' ? createHash('sha256').update(await readFile(join(root, 'payload'))).digest('hex') : digest;
        await writeFile(join(root, 'hash'), hash);
        await writeFile(join(bin, 'curl'), `#!/bin/sh
url=; output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    https://*) url=$1; shift ;;
    --output) output=$2; shift 2 ;;
    *) shift ;;
  esac
done
case "$url" in
  */SHA256SUMS)
    : > "$output"
    [ "$SCENARIO" != missing-checksum ] || exit 0
    for os in darwin linux; do for arch in arm64 amd64; do
      printf '%s  koto-cli-%s-%s\\n' "$(cat "$MOCK_ROOT/hash")" "$os" "$arch" >> "$output"
      if [ "$SCENARIO" = duplicate-checksum ]; then printf '%s  koto-cli-%s-%s\\n' "$(cat "$MOCK_ROOT/hash")" "$os" "$arch" >> "$output"; fi
    done; done ;;
  */koto-cli-*)
    [ "$SCENARIO" != download-failure ] || exit 22
    cp "$MOCK_ROOT/payload" "$output"
    if [ "$SCENARIO" = corrupt ]; then printf broken >> "$output"; fi ;;
  *) exit 99 ;;
esac
`, { mode: 0o755 });
        const old = join(dest, 'koto');
        if (scenario === 'symlink') {
          await writeFile(join(root, 'existing'), 'original');
          await symlink(join(root, 'existing'), old);
        } else { await writeFile(old, 'original'); }
        const result = spawnSync('sh', [installer, '--version', version, '--dir', dest], {
          env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, MOCK_ROOT: root, SCENARIO: scenario }, encoding: 'utf8', timeout: 10000,
        });
        assert.equal(result.status === 0, scenario === 'ok', result.stdout + result.stderr);
        assert.equal(await readFile(old, 'utf8'), scenario === 'ok' ? payload : 'original');
        if (scenario === 'ok' || scenario === 'install-failure') assert.equal(await readFile(join(root, 'dispatch'), 'utf8'), `${await realpath(old)}\n${digest}\n`);
        assert.deepEqual(await readdir(dest), ['koto']);
      } finally { await rm(root, { recursive: true, force: true }); }
    });
  }
});
