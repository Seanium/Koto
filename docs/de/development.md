# Entwicklung

[English](../development.md) · [简体中文](../zh/development.md) · [繁體中文](../zh-TW/development.md) · [日本語](../ja/development.md) · [Deutsch](development.md)

Desktop-App und CLI liegen in `app/`. Die Desktop-App verwendet Wails v3 mit der System-WebView, Go, modernc SQLite, React und zustand. Das Backend enthält keine Node.js-Laufzeit.

## Verzeichnisstruktur

- `app/frontend/`: Oberfläche, Übersetzungen und Frontendtests.
- `app/internal/`: Konnektoren, Kopierplanung, Vergleich und lokale Speicherung.
- `app/build/`, `app/scripts/`: native Pakete und Prüfungen.
- `site/`: Projektwebsite; `docs/`: Nutzungs- und Entwicklungsanleitungen in allen App-Sprachen.
- `updates/`: Cloudflare-Updatedienst und festgelegte lokale Wrangler-Version; siehe [Deployment](../../updates/README.md).
- `scripts/`: schreibgeschützte Quellcodeprüfung und Pflege der Drittanbieterlizenzen.
- `app/.tools/`, `app/.cache/`: von Git ignorierte lokale Umgebungen und Testnachweise; `app/bin/`: erzeugte Pakete.

## Voraussetzungen

- Go ab Version 1.26, passend zu `app/go.mod`.
- Node.js und npm passend zu `app/frontend/package-lock.json`; für den folgenden TypeScript-Testbefehl Node.js ab Version 24 verwenden.
- Für Wails-Entwicklung und generierte Bindings eine Wails-CLI-Version passend zu `app/go.mod`.
- Den nativen Compiler und die System-WebView-Entwicklungspakete, die Wails auf dem Zielbetriebssystem benötigt.

`app/env.sh` nutzt projektlokales Go unter `app/.tools/go` und Wails unter `app/.tools/bin`. Diese Werkzeuge vor den folgenden Befehlen bereitstellen.

## Bauen und prüfen

Im Stammverzeichnis des Repositorys:

```sh
source app/env.sh
export npm_config_cache="$PWD/app/.cache/npm"
export TMPDIR="$PWD/app/.cache/tmp"
mkdir -p "$TMPDIR"
npm --prefix app/frontend ci
npm --prefix app/frontend run build
go -C app test -race ./...
node --experimental-strip-types --test app/frontend/tests/*.test.ts
node --test site/*.test.mjs updates/*.test.mjs
go -C app build -o bin/koto ./cmd/koto
```

Baue das Frontend vor dem Testen des Desktop-Pakets, da Go `frontend/dist` einbettet. Der Race Detector benötigt eine unterstützte Plattform und eine native C-Toolchain. Ändert sich eine Go-Service-Signatur, generiere die TypeScript-Bindings aus `app/` neu:

```sh
wails3 generate bindings -clean=true -ts -i
```

Die optionale DeepSeek-Logprüfung benötigt das lokale Paket und Node.js. Das Paket mit dem Task `tools:dsh` installieren; andernfalls wird diese Prüfung übersprungen.

## Isolierte Sitzungen

Für die Arbeit an der Oberfläche ausschließlich synthetische Daten erzeugen:

```sh
cd app
go run ./cmd/seed
KOTO_APP_SANDBOX="$PWD/.sandbox" wails3 dev -config ./build/config.yml
```

Das Seed-Werkzeug erstellt `.sandbox` mit einer `.koto-sandbox`-Markierung. Bei erneuter Ausführung leert es nur eine markierte Sandbox und verweigert ein bereits vorhandenes unmarkiertes Verzeichnis. Übergib dem Seed-Werkzeug nie ein persönliches Agent-Verzeichnis.

`app/scripts/preview-mac.sh` baut eine macOS-Vorschau mit der synthetischen Sandbox.

Optionale `agy_native`- und `codex_native`-Tests benötigen ausdrücklich angegebene Testverzeichnisse, CLI-Pfade und Ausgabeordner. Verwende dafür keine vorhandenen persönlichen Sitzungen. Standardtests nutzen eigene temporäre Verzeichnisse.

## Pakete erstellen

Den Paketbefehl auf dem Zielbetriebssystem ausführen. Für Intel/x64 `arm64` durch `amd64` ersetzen:

```sh
app/scripts/package-mac.sh arm64
app/scripts/linux-package.sh arm64
app/scripts/package-windows.sh arm64
app/scripts/package-cli.sh linux arm64
```

Für macOS-DMGs sind außerdem Python 3 und `uv` nötig (`KOTO_UV` kann auf eine projektlokale Datei zeigen). Das Skript installiert festgelegte Build-Abhängigkeiten in `app/.tools/dmgbuild` und nutzt `app/.cache/uv` als Download-Cache. Systempakete bleiben unverändert.

Die Ausgabe liegt in `app/bin/release/`. macOS nutzt Ad-hoc-Signaturen. Linux benötigt GTK3, WebKitGTK 4.1, GCC, pkg-config und dpkg-deb. Unter Windows entstehen separate ausführbare Dateien für App und CLI.

Die Projektseite benötigt keinen Build. Vorschau starten:

```sh
UV_CACHE_DIR="$PWD/app/.cache/uv" uv venv app/.cache/release-venv
app/.cache/release-venv/bin/python -m http.server 4173 --bind 127.0.0.1 --directory site
```

Pages und [native CI](ci.md) werden manuell gestartet. CI erfasst Tests, Paketerstellung und WebView-Ergebnisse getrennt; Alle drei Plattformen sind standardmäßig aktiviert.

Für die Website-Vorschau unter Windows `app\.cache\release-venv\Scripts\python.exe` verwenden.
