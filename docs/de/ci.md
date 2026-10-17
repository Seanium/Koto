# Native CI

[English](../ci.md) · [简体中文](../zh/ci.md) · [繁體中文](../zh-TW/ci.md) · [日本語](../ja/ci.md) · [Deutsch](ci.md)

`.github/workflows/ci.yml` wird manuell gestartet. Der Workflow testet Frontend und Go-Pakete, baut Pakete und versucht auf jedem Host einen echten nativen WebView-Smoke-Test. Er hat ausschließlich die Berechtigung `contents: read` und lädt CI-Artefakte hoch. Er erstellt keine Releases, pusht keinen Code und veröffentlicht die Website nicht.

Die Standardmatrix umfasst macOS und Linux. Windows-Jobs laufen nur mit ausdrücklich aktiviertem `include_windows`.

| Ziel | Nativer Runner-Label |
| --- | --- |
| macOS arm64 | `macos-15` |
| macOS Intel / amd64 | `macos-15-intel` |
| Linux arm64 | `ubuntu-24.04-arm` |
| Linux amd64 | `ubuntu-24.04` |
| Windows arm64 | `windows-11-arm` |
| Windows amd64 | `windows-2025` |

Die Runner-Bezeichnungen folgen der [GitHub-Liste gehosteter Runner](https://docs.github.com/en/actions/reference/runners/github-hosted-runners). Der Workflow prüft die tatsächliche Host- und Werkzeugarchitektur; Cross-Kompilierung ersetzt keinen fehlenden nativen Runner.

Go, Node, Wails und NSIS liegen in `app/.tools`, Caches und Testnachweise in `app/.cache`. Downloads werden per SHA-256 geprüft. Linux-Systemabhängigkeiten werden nur auf dem temporären Runner installiert.

## Bedeutung der Ergebnisse

Jedes Artefakt enthält `stages.json` mit getrennten Ergebnissen für Frontendtests, Go-Tests, Paketerstellung und native GUI-Prüfung. Die Pakete sind CI-Vorschauen. Ein erzeugter Installer belegt keine erfolgreiche Installation; eine cross-kompilierte Datei belegt keinen nativen GUI-Lauf.

macOS prüft App-Bundle und DMG und startet anschließend die native WebView mit synthetischen Sitzungen. Linux entpackt das DEB und führt die GTK3/WebKitGTK-App unter Xvfb aus. Das Testergebnis muss JSON enthalten mit `passed: true`, passendem Betriebssystem und passender Architektur sowie abgeschlossenen App-Prüfungen. Ein Prozessende mit Status 0 reicht allein nicht aus.

Windows-GUI-Tests benötigen die [WebView2 Runtime](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution) und einen interaktiven Desktop. Fehlt eine Voraussetzung, schlägt die GUI-Prüfung fehl. Gebaute Pakete und Teilergebnisse bleiben zur Diagnose verfügbar.
