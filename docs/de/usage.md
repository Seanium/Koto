# koto verwenden

[English](../usage.md) · [简体中文](../zh/usage.md) · [繁體中文](../zh-TW/usage.md) · [日本語](../ja/usage.md) · [Deutsch](usage.md)

koto liest Sitzungen aus den in den Einstellungen aktivierten Agent-Verzeichnissen. Die Suche findet indizierte Titel, Sitzungs-IDs, erste Fragen, Projektpfade und Agent-Namen.

## Gespräche kopieren

1. Eine Sitzung öffnen und **Kopierbereich wählen** anklicken. Anfangs ist alles ausgewählt.
2. Die Auswahl beibehalten oder per Klick und Ziehen Gesprächsrunden, Fragen und Antworten auswählen.
3. Den Ziel-Agent wählen und die Umwandlungshinweise prüfen. Nach dem Kopieren die neue Sitzung öffnen und fortsetzen.

Auch Kopien zum selben Agent sind möglich. Jede Kopie erstellt eine neue Sitzung, erhält die Reihenfolge der ausgewählten Nachrichten und lässt die Quelle unverändert. Das Ziel nutzt seine eigenen Modelle, Anbieter, Berechtigungen und Systemeinstellungen; System- und Entwickleranweisungen der Quelle werden nicht übernommen.

Fehlende Dateien und nicht unterstützte Inhalte werden in den Umwandlungshinweisen gemeldet. Details stehen unter [Anhänge](compatibility.md).

## Sitzungen vergleichen

Unter **Sitzungen vergleichen** 2–32 Sitzungen desselben Agents auswählen. Der Baum zeigt gemeinsame Verläufe und Abzweigungen. Zwei beliebige Sitzungen lassen sich Zeile für Zeile vergleichen.

Ein gespeicherter Vergleich enthält nur Sitzungsreferenzen. Name und Sitzungsliste lassen sich ändern; das Löschen des Vergleichs lässt die Sitzungen bestehen. Der Baum wird aus dem aktuellen Inhalt berechnet. Verschobene oder gelöschte Sitzungen können deshalb nicht mehr erreichbar sein.

## Mit dem Agent kopieren

App und CLI gibt es als getrennte Downloads unter [Releases](https://github.com/Seanium/koto/releases). Die CLI braucht weder App noch WebView. `--help` erklärt dem Agent den Ablauf.

| Plattform | CLI einrichten |
| --- | --- |
| macOS / Linux | `koto-cli-<os>-<arch>` herunterladen, in `koto` umbenennen und `chmod +x koto` ausführen. |
| Windows | `koto-cli-windows-<arch>.exe` herunterladen und in `koto.exe` umbenennen. |

Die CLI in einen Ordner im PATH legen oder dem Agent den vollständigen Pfad geben. Windows-Befehle zum Fortsetzen in PowerShell ausführen. Die Windows-App ist eine direkt ausführbare `.exe`; die Linux-App gibt es als Binärdatei und DEB, jeweils ohne CLI.

Dann sagen: **„Kopiere dieses Gespräch mit koto nach pi.“**

Wenn OpenCode keine Host-Sitzungs-ID liefert, prüft koto den laufenden Werkzeugaufruf. Ein Skill ist dafür nicht nötig.

Die Erkennung nutzt `CODEX_THREAD_ID`, `PI_SESSION_ID`, `DSH_SESSION_ID`, `ANTIGRAVITY_CONVERSATION_ID` oder `CLAUDE_CODE_SESSION_ID`, sofern der Host eine davon bereitstellt. Andernfalls die genaue Angabe `agent:session-id` direkt oder über `KOTO_SESSION` verwenden. Ein ausdrücklich gesetztes `KOTO_SESSION` hat Vorrang; widersprüchliche Host-IDs werden abgelehnt. koto wählt keine Sitzung nach Aktualität oder Projektordner aus.

Die Antwort nennt die kopierte Anzahl, Umwandlungshinweise und den Befehl zum Fortsetzen. Die noch laufende Runde einschließlich des Kopierauftrags wird ausgelassen. Die Quelle bleibt unverändert; der Ziel-Agent wird nicht automatisch gestartet.

DeepSeek Harness setzt die gewählte Sitzung im Headless-Modus fort. Den Befehl starten und die nächste Nachricht eingeben. Unter macOS/Linux in einer neuen Zeile mit **Strg+D** senden; unter Windows die Eingabeaufforderung mit **Enter** bestätigen. Jeder Aufruf beantwortet eine Nachricht.

Für OpenCode 1.19.3 unter Windows ARM bitte die offizielle x64-Version verwenden; die native ARM-Version kann ihre Terminaloberfläche nicht starten.

## CLI und MCP

Die Beispiel-IDs durch tatsächliche Sitzungs-IDs ersetzen. `--dry-run` zeigt eine Kopiervorschau, `--help` alle Optionen.

```sh
koto scan
koto threads
koto show 'claude-code:<session-id>'
koto current --json
koto copy --current --to pi --json
koto copy 'claude-code:<session-id>' --to pi --dry-run
koto copy 'claude-code:<session-id>' --to pi --json
koto analyze 'claude-code:<session-a>' 'claude-code:<session-b>' --json
```

Ist `koto` nicht in PATH, den vollständigen Pfad aus der Tabelle verwenden. `--json` liefert `state: "copied"` nur nach erfolgreichem Kopieren, bei einer Vorschau `state: "preview"`. Fehler liefern eine Fehlermeldung und einen Exit-Code ungleich null. Einzelne Fragen oder Antworten lassen sich in der App auswählen.

`koto trash <selector>` verschiebt eine Sitzung in den Papierkorb oder das Archiv ihres Agents. `koto restore <trash-id>` stellt sie wieder her, ohne vorhandene Pfade zu überschreiben. Für Tests mit `--data`, Agent-Verzeichnissen und `--only` entbehrliche Verzeichnisse festlegen.

`koto mcp` stellt die schreibgeschützten Werkzeuge `list_sessions` und `get_session` über stdio JSON-RPC bereit.

## Einstellungen und Speicherung

Als Sprache stehen Englisch, vereinfachtes und traditionelles Chinesisch, Japanisch, Deutsch oder die Systemsprache zur Wahl. Das Erscheinungsbild kann System, Hell oder Dunkel sein.

koto speichert Index, Einstellungen, Vergleichsreferenzen und Papierkorbinformationen lokal. Gesprächssicherungen und Kopierverläufe werden nicht gespeichert. Kopierte Anhänge gehören zur neuen Agent-Sitzung. Kopiert wird ausschließlich manuell.
