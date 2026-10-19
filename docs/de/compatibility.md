# Sitzungsformate und Anhänge

[English](../compatibility.md) · [简体中文](../zh/compatibility.md) · [繁體中文](../zh-TW/compatibility.md) · [日本語](../ja/compatibility.md) · [Deutsch](compatibility.md)

Die Tabellen zeigen unterstützte Speicherformate. Agent Harness-Updates können diese ändern. Ob Anhänge erkannt werden, hängt vom Zielmodell ab.

| Agent Harness | Lesen | Kopie erstellen |
| --- | --- | --- |
| Claude Code / Claude Desktop (Code) | JSONL und native Verzweigungen | Neue UUID und natives JSONL |
| pi | v3-JSONL-Bäume | Neue Sitzung mit neuer ID- und Elternkette |
| Codex | rollout JSONL / zstd | Neuer nativer Rollout, danach Leseprüfung über app-server |
| DeepSeek Harness | v4 JSONL / zstd | Neues Verzeichnis und natives komprimiertes Protokoll |
| Antigravity CLI | SQLite und Protobuf-Verläufe | Neue Gesprächsdatenbank und Zusammenfassungseintrag |
| OpenCode | SQLite v1 (`session`, `message`, `part`) | Neue Sitzungszeilen in der nativen Datenbank |
| Cursor CLI | SQLite `store.db` je Sitzung | Neue native CLI-Sitzung |
| Cursor Desktop | Gespräche und Nachrichten-Blobs in `state.vscdb` | Neues IDE-Gespräch, in der App öffnen |
| WorkBuddy / WorkBuddy AI | Getrennte China-/internationale Ordner, SQLite-Index und JSONL | Neue Sitzung unter einem vorhandenen nativen Konto |
| ZCode | Gemeinsame App-/CLI-Historie in SQLite und App-Aufgabenindex | Neue native Sitzung samt Aufgabenverweis |

Unterstützt wird das v1-Format von OpenCode 1.19.3. v2-Speicher wird ausdrücklich abgelehnt. Kopien nach OpenCode benötigen dessen CLI und ein konfiguriertes Standardmodell; beim Fortsetzen wird das Zielmodell verwendet.

Jede Kopie erstellt eine neue native Sitzung; bestehende Sitzungs-IDs und einzelne Dateien werden nicht überschrieben. Der Ziel-Agent Harness lädt seine eigenen Einstellungen; System-, Entwickler-, Umgebungs-, Konfigurations- und Berechtigungskontext der Quelle werden nicht kopiert. Werkzeuge und Denkprotokolle können in lesbaren Text umgewandelt werden, wenn das Ziel keine passende native Darstellung bietet.

Cursor Desktop und CLI haben getrennte Speicher und Fortsetzungswege. ZCode unterstützt den nativen Agent Harness der regulären Version, nicht Preview oder extern gestartete Agent Harnesses. WorkBuddy und WorkBuddy AI sind getrennte Ziele. Historische Kontokennungen ändern nicht die Anmeldung beim Fortsetzen.

Claude Desktop unterstützt lokale Code-Sitzungen als Quelle und Ziel. Vorhandene lokale Cowork-Protokolle lassen sich lesen und in unterstützte Ziele kopieren; neue Cowork-Aufgaben werden nicht erstellt. Der lokale Scan erfasst keinen Chat-Verlauf aus der Cloud.

Falls die kopierte Code-Sitzung in Claude Desktop nicht erscheint, starten Sie die App neu, um die Sitzungsliste zu aktualisieren.

## Anhänge

| Material | Claude Code | pi | Codex | DeepSeek Harness | Antigravity CLI | OpenCode |
| --- | --- | --- | --- | --- | --- | --- |
| PNG, JPEG, GIF, WebP | Natives Bild | Natives Bild | Natives Bild | Nativer Bildanhang | Native Media | Native Datei |
| PDF, UTF-8-Text | Natives Dokument | Vollständige Datei | Vollständige Datei | Nativer Dateianhang | Native Media | Native Datei |
| Audio | Vollständige Datei | Vollständige Datei | Natives Audio | Nativer Dateianhang | Native Media | Native Datei |
| DOCX, ZIP, JSON, andere Binärdateien | Vollständige Datei | Vollständige Datei | Vollständige Datei | Nativer Dateianhang | Native Media | Native Datei |

| Weiterer Agent Harness | Anhangsformat |
| --- | --- |
| Cursor CLI / Cursor Desktop | Native Bilder; vollständige Dateien oder Dateireferenzen |
| WorkBuddy / WorkBuddy AI | Native Bilder und Benutzer-Dateireferenzen; geprüfte Referenzen für Werkzeugdateien |
| ZCode | Native Medien und vollständige Dateien in der Zielsitzung |

Wenn eine Rolle oder ein Ziel eine Datei nicht einbetten kann, speichert koto die vollständigen Bytes im Anhangsverzeichnis der neuen Sitzung und fügt eine Dateireferenz hinzu. Anhänge aus Werkzeugergebnissen bleiben auch dann erhalten, wenn das Werkzeugprotokoll in Text umgewandelt wird.

- Anhänge werden aus erkannten nativen Anhangsquellen gelesen, nicht aus beliebigen in Nachrichten erwähnten Pfaden.
- Beim erneuten Kopieren von koto-verwalteten Dateireferenzen werden Verzeichnis, tatsächlicher Pfad, Größe und SHA-256 geprüft. Externe symbolische Links, Pfadtraversierung und veränderte Dateien werden abgelehnt.
- Fehlende Blobs werden gemeldet. Entfernte URIs bleiben Referenzen und werden nicht zur Offline-Speicherung heruntergeladen.
- Enthält die ursprüngliche Sitzung nur einen Teil eines Textanhangs, kopiert koto diesen gespeicherten Ausschnitt und kennzeichnet ihn als unvollständig. Fehlende Inhalte werden nicht aus der aktuellen Datei auf dem Datenträger ergänzt.
- Einzelne Vorschauen sind auf 25 MiB begrenzt, das Lesen von Anhängen auf 256 MiB. Die Grenzen führen nicht dazu, dass eine Datei als abgeschnittene Textzusammenfassung gespeichert wird.

## Grenzen der Verifikation

Die Standardtests verwenden synthetische Fixtures und entbehrliche Agent Harness-Verzeichnisse. Sie prüfen Kopien zwischen unterstützten Agent Harnesses, Kopien zum selben Agent Harness, ausdrückliche Auswahlen, wiederholte neue Sitzungen, Pfadkonflikte, Anhänge und die Integrität beim erneuten Kopieren.

Tests der nativen Gesprächsfortsetzung hängen vom installierten Agent Harness, Modell und Anbieter ab und laufen getrennt von der Standardsuite. Nicht alle Modelle, Format-Rollen-Kombinationen und Desktop-Plattformen sind bisher abgedeckt.
