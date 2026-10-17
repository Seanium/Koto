# koto project page

Buildless HTML, CSS, and JavaScript. Supports English, 简体中文, 繁體中文, 日本語, and Deutsch, with System, Light, and Dark appearance. No framework, tracking, remote assets, or external fonts.

Screenshots are unedited real app captures using synthetic sandbox sessions, with no visible home paths or user conversations. Store each language, theme, and view at `assets/screenshots/{locale}-{light|dark}-{copy|tree|diff}.png`. Missing images show a placeholder. Images display inline only. After replacing captures, refresh the content revision in `main.js` and its script URL in `index.html`.

The screenshot tabs use Primer Octicons `copy-24`, `git-branch-24`, and `diff-24` (MIT), included inline without a runtime dependency.

Agent SVGs come from LobeHub Icons via magpie. The speech-bubble mark and favicon contain the Unicode 💬 character, rendered by the visitor's system emoji font; neither embeds a font or glyph image. Attribution is in `THIRD_PARTY_NOTICES.md` and `licenses/assets/`.

From the repository root:

```sh
UV_CACHE_DIR="$PWD/app/.cache/uv" uv venv app/.cache/release-venv
app/.cache/release-venv/bin/python -m http.server 4173 --bind 127.0.0.1 --directory site
```

Open `http://127.0.0.1:4173`. Browser-local preferences persist across reloads; System follows OS changes. Screenshot tabs support arrow keys, Home, and End.

The hero and CLI dialogue animate in loops, pause while hidden, and show complete text with reduced motion. Static text remains available to assistive technology, and typed characters reserve their space to prevent layout shifts.

Release version, repository, and publication state live in `publication.mjs`. Enable `published` after all listed assets exist; otherwise no download URLs are generated. The Pages workflow is manual.

Download suggestions use browser-provided OS and CPU hints in memory only. Reduced Mac/Linux user agents never determine architecture; unknown CPUs require a choice. Linux suggestions use tar.gz because the distribution is unknown. Detection never overrides a manual selection or starts a download.
