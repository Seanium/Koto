# Source maintenance

The root documentation and `licenses/` are the canonical public files. There is no separate release copy to keep in sync. App packaging scripts remain in `app/scripts/`.

Use a project-local Python environment:

```sh
UV_CACHE_DIR="$PWD/app/.cache/uv" uv venv app/.cache/release-venv
app/.cache/release-venv/bin/python scripts/check-source.py
```

`check-source.py` reads the current tracked and unignored files. It checks the source allowlist, symlinks, common secret and personal-path patterns, documentation links, required translations, and license hashes. Its JSON output includes file hashes. It does not copy, modify, publish, or inspect Git history.

The working repository is also the publication source. Keep tools, caches, real sessions, binaries, test evidence, and design drafts in ignored directories. Local verification records belong in `app/.cache/release-review/`.

After changing dependencies, provision the pinned packages and refresh their notices:

```sh
app/.cache/release-venv/bin/python scripts/collect-licenses.py
```

The collector reads installed project-local Go/npm dependencies and downloads the upstream licenses for the included Agent Harness marks. `licenses/INDEX.json` records package versions and file hashes. It refuses to replace a nonempty license directory without that index.
