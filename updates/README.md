# Update service

Cloudflare Worker for the App, CLI and terminal installers. Binaries remain on GitHub Releases. The website stays on GitHub Pages.

- `/api/releases`: latest stable and latest overall release, including asset URLs, sizes and SHA-256 digests.
- `/api/latest`: latest stable release; `?channel=beta` includes prereleases. `?format=tag` returns a plain version tag for the shell installer.
- `/api/releases/tags/v0.1.0`: exact version metadata, including rollback packages.
- `/download/<asset>`: redirects to the verified release asset.

The Worker reads GitHub's API and `SHA256SUMS`, caches results for five minutes, and falls back to the public release redirect and Atom feed. Fallback responses cache for one minute. A last successful response has a 30-day cache TTL, subject to Cloudflare eviction; it is marked `stale` when upstream metadata cannot be refreshed. Clients must not interpret stale metadata as proof that they are up to date. Errors are not cached. No database, KV or R2 is required.

From the repository root:

```sh
npm --prefix updates ci --cache app/.cache/npm
npm --prefix updates test
sh updates/wrangler.sh dev --local --port 8788 --persist-to ../app/.cache/cloudflare/state
sh updates/wrangler.sh deploy --dry-run --outdir ../app/.cache/cloudflare/build
```

Wrangler is pinned in `updates/package-lock.json`. Its wrapper keeps authentication, logs and caches under ignored `app/.cache/cloudflare/`. It does not install a global CLI or change shell configuration.

Deploy after tests pass:

```sh
sh updates/wrangler.sh login --device --scopes account:read user:read workers:write workers_scripts:write workers_routes:write zone:read
sh updates/wrangler.sh deploy
```

The configured custom domain is `updates.kotoai.site`. Publishing a GitHub Release updates the feed after cache expiry; a new Worker deployment is needed only when its code changes. An optional `GITHUB_TOKEN` Worker secret raises GitHub API limits. Set it with `sh updates/wrangler.sh secret put GITHUB_TOKEN`; never put credentials in source, client binaries or the public response. The service works without it through the public fallback.

CI runs the dependency-free Worker tests alongside the website tests. A local Wrangler run verifies the Cloudflare runtime; successful deployment and live endpoint checks are separate release requirements.
