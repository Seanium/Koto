# Sitzungsformate und Anhänge

[English](../compatibility.md) · [简体中文](../zh/compatibility.md) · [繁體中文](../zh-TW/compatibility.md) · [日本語](../ja/compatibility.md) · [Deutsch](compatibility.md)

Die Tabellen zeigen unterstützte Speicherformate. Agent-Updates können diese ändern. Ob Anhänge erkannt werden, hängt vom Zielmodell ab.

| Agent | Lesen | Kopie erstellen |
| --- | --- | --- |
| Claude Code / Desktop Code | JSONL und native Verzweigungen | Neue UUID und natives JSONL |
| pi | v3-JSONL-Bäume | Neue Sitzung mit neuer ID- und Elternkette |
| Codex | rollout JSONL / zstd | Neuer nativer Rollout, danach Leseprüfung über app-server |
| DeepSeek Harness | v4 JSONL / zstd | Neues Verzeichnis und natives komprimiertes Protokoll |
| Antigravity CLI | SQLite und Protobuf-Verläufe | Neue Gesprächsdatenbank und Zusammenfassungseintrag |

Jede Kopie erstellt eine neue native Sitzung. Das Schreiben des Ziels wird verweigert, wenn der Pfad bereits existiert. Der Ziel-Agent lädt seine eigenen Einstellungen; System-, Entwickler-, Umgebungs-, Konfigurations- und Berechtigungskontext der Quelle werden nicht kopiert. Werkzeuge und Denkprotokolle können in lesbaren Text umgewandelt werden, wenn das Ziel keine passende native Darstellung bietet.

## Anhänge

| Material | Claude Code | pi | Codex | DeepSeek Harness | Antigravity CLI |
| --- | --- | --- | --- | --- | --- |
| PNG, JPEG, GIF, WebP | Natives Bild | Natives Bild | Natives Bild | Nativer Bildanhang | Native Media |
| PDF, UTF-8-Text | Natives Dokument | Vollständige Datei | Vollständige Datei | Nativer Dateianhang | Native Media |
| Audio | Vollständige Datei | Vollständige Datei | Natives Audio | Nativer Dateianhang | Native Media |
| DOCX, ZIP, JSON, andere Binärdateien | Vollständige Datei | Vollständige Datei | Vollständige Datei | Nativer Dateianhang | Native Media |

Wenn eine Rolle oder ein Ziel eine Datei nicht einbetten kann, speichert koto die vollständigen Bytes im Anhangsverzeichnis der neuen Sitzung und fügt eine Dateireferenz hinzu. Anhänge aus Werkzeugergebnissen bleiben auch dann erhalten, wenn das Werkzeugprotokoll in Text umgewandelt wird.

- Anhänge werden aus erkannten nativen Anhangsquellen gelesen, nicht aus beliebigen in Nachrichten erwähnten Pfaden.
- Beim erneuten Kopieren von koto-verwalteten Dateireferenzen werden Verzeichnis, tatsächlicher Pfad, Größe und SHA-256 geprüft. Externe symbolische Links, Pfadtraversierung und veränderte Dateien werden abgelehnt.
- Fehlende Blobs werden gemeldet. Entfernte URIs bleiben Referenzen und werden nicht zur Offline-Speicherung heruntergeladen.
- Einzelne Vorschauen sind auf 25 MiB begrenzt, das Lesen von Anhängen auf 256 MiB. Die Grenzen führen nicht dazu, dass eine Datei als abgeschnittene Textzusammenfassung gespeichert wird.

## Grenzen der Verifikation

Die Standardtests verwenden synthetische Fixtures und entbehrliche Agent-Verzeichnisse. Sie prüfen Kopien zwischen unterstützten Agents, Kopien zum selben Agent, ausdrückliche Auswahlen, wiederholte neue Sitzungen, Pfadkonflikte, Anhänge und die Integrität beim erneuten Kopieren.

Tests der nativen Gesprächsfortsetzung hängen vom installierten Agent, Modell und Anbieter ab und laufen getrennt von der Standardsuite. Nicht alle Modelle, Format-Rollen-Kombinationen und Desktop-Plattformen sind bisher abgedeckt.
