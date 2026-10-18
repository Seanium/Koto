# koto verwenden

[English](../usage.md) · [简体中文](../zh/usage.md) · [繁體中文](../zh-TW/usage.md) · [日本語](../ja/usage.md) · [Deutsch](usage.md)

koto liest Sitzungen aus den in den Einstellungen aktivierten Agent-Verzeichnissen. Die Suche findet indizierte Titel, Sitzungs-IDs, erste Fragen, Projektpfade und Agent-Namen.

## Gespräche kopieren

1. Eine Sitzung öffnen und **Kopierbereich wählen** anklicken. Anfangs ist alles ausgewählt.
2. Die Auswahl beibehalten oder per Klick und Ziehen Gesprächsrunden, Fragen und Antworten auswählen.
3. Den Ziel-Agent wählen und die Umwandlungshinweise prüfen. Nach dem Kopieren die neue Sitzung öffnen und fortsetzen.

Auch Kopien zum selben Agent sind möglich. Jede Kopie erstellt eine neue Sitzung, erhält die Reihenfolge der ausgewählten Nachrichten und lässt die Quelle unverändert. Das Ziel nutzt seine eigenen Modelle, Anbieter, Berechtigungen und Systemeinstellungen; System- und Entwickleranweisungen der Quelle werden nicht übernommen.

Fehlende Dateien und nicht unterstützte Inhalte werden in den Umwandlungshinweisen gemeldet. Details stehen unter [Anhänge](compatibility.md).

## Konten und Ordner

Die Sitzungsliste lässt sich nach Speicherordner und erkanntem Konto filtern. Weitere Agent-Ordner können in den Einstellungen hinzugefügt werden. Sitzungen mit unbekanntem Konto bleiben sichtbar; Anbieter- und Modellnamen gelten nicht als Kontoidentität.

Im Kopierdialog wählst du den Zielordner. Beim selben Produkt ist der Quellordner voreingestellt. Ein Konto wird nur gewählt, wenn das Ziel Sitzungen nach Konto speichert; ein einzelnes gültiges Konto wird automatisch ausgewählt. Zum Fortsetzen nutzt das Ziel seine eigene Anmeldung oder API-Konfiguration. koto kopiert keine Zugangsdaten.

```sh
koto profiles --json
koto threads --profile '<profile-id>' --json
koto copy '@<source-profile>:<session-id>' --to pi --to-profile '<target-profile>' --json
```

Verwende die von koto ausgegebenen Profil- und Sitzungskennungen. `--to-root <Ordner>` registriert einen vorhandenen Agent-Datenordner als Ziel. `--to-account <id>` gilt nur für Ziele mit Kontoauswahl. Bei gleichen Sitzungs-IDs in verschiedenen Ordnern muss das Profil angegeben werden.

## Sitzungen vergleichen

Unter **Sitzungen vergleichen** 2–32 Sitzungen desselben Agents auswählen. Der Baum zeigt gemeinsame Verläufe und Abzweigungen. Zwei beliebige Sitzungen lassen sich Zeile für Zeile vergleichen.

Ein gespeicherter Vergleich enthält nur Sitzungsreferenzen. Name und Sitzungsliste lassen sich ändern; das Löschen des Vergleichs lässt die Sitzungen bestehen. Der Baum wird aus dem aktuellen Inhalt berechnet. Verschobene oder gelöschte Sitzungen können deshalb nicht mehr erreichbar sein.

## Mit dem Agent kopieren

App und CLI gibt es als getrennte Downloads unter [Releases](https://github.com/Seanium/koto/releases). Die CLI braucht weder App noch WebView. `--help` erklärt dem Agent den Ablauf.

Die CLI lässt sich auch im Terminal installieren:

macOS / Linux:

```sh
curl -fsSL https://kotoai.site/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://kotoai.site/install.ps1 | iex
```

Standardordner: `~/.local/bin` unter macOS/Linux, `%LOCALAPPDATA%\Programs\koto\bin` unter Windows. Diesen Ordner zum PATH hinzufügen, falls der Installer dazu auffordert.

Die CLI in einen Ordner im PATH legen oder dem Agent den vollständigen Pfad geben. Windows-Befehle zum Fortsetzen in PowerShell ausführen. Die Windows-App ist eine direkt ausführbare `.exe`; die Linux-App gibt es als Binärdatei und DEB, jeweils ohne CLI. Die Linux-Binärdatei vor dem Start mit `chmod +x koto-linux-<arch>` ausführbar machen.

Dann sagen: **„Kopiere dieses Gespräch mit koto nach pi.“**

Wenn OpenCode keine Host-Sitzungs-ID liefert, prüft koto den laufenden Werkzeugaufruf.

Die Erkennung nutzt `CODEX_THREAD_ID`, `PI_SESSION_ID`, `DSH_SESSION_ID`, `ANTIGRAVITY_CONVERSATION_ID`, `CURSOR_CONVERSATION_ID` oder `CLAUDE_CODE_SESSION_ID`, sofern der Host eine davon bereitstellt. Andernfalls die genaue Angabe `agent:session-id` direkt oder über `KOTO_SESSION` verwenden. Ein ausdrücklich gesetztes `KOTO_SESSION` hat Vorrang; widersprüchliche Host-IDs werden abgelehnt. koto wählt keine Sitzung nach Aktualität oder Projektordner aus.

WorkBuddy verwendet `CODEBUDDY_SESSION_ID` zusammen mit dem passenden `WORKBUDDY_CONFIG_DIR`. Die Sitzungs-ID allein bestimmt kein WorkBuddy-Profil.

Die Antwort nennt die kopierte Anzahl, Umwandlungshinweise und die Schritte zum Fortsetzen im Ziel-Agent. Die noch laufende Runde einschließlich des Kopierauftrags wird ausgelassen. Die Quelle bleibt unverändert; der Ziel-Agent wird nicht automatisch gestartet.

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

Ist `koto` nicht in PATH, den vollständigen Pfad der installierten Datei verwenden. `--json` liefert `state: "copied"` nur nach erfolgreichem Kopieren, bei einer Vorschau `state: "preview"`. Fehler liefern eine Fehlermeldung und einen Exit-Code ungleich null. Einzelne Fragen oder Antworten lassen sich in der App auswählen.

`koto mcp` stellt die schreibgeschützten Werkzeuge `list_sessions` und `get_session` über stdio JSON-RPC bereit.

## Updates

In der App unter **Einstellungen → Allgemein** nach Updates suchen. Bei einer neuen Version **Aktualisieren und neu starten** wählen.

CLI aktualisieren:

```sh
koto update --check
koto update
```

App und CLI prüfen neue Versionen über den Cloudflare-Updatedienst, laden ihre separaten Pakete von GitHub Releases herunter und prüfen vor der Installation SHA-256. Die Prüfung berücksichtigt stabile Versionen und gekennzeichnete Vorabversionen. Installierte Versionen werden nicht heruntergestuft. Mit `--channel stable` prüft die CLI nur stabile Versionen.

## Einstellungen und Speicherung

Als Sprache stehen Englisch, vereinfachtes und traditionelles Chinesisch, Japanisch, Deutsch oder die Systemsprache zur Wahl. Das Erscheinungsbild kann System, Hell oder Dunkel sein.

koto speichert Index, Einstellungen und Vergleichsreferenzen lokal. Gesprächssicherungen und Kopierverläufe werden nicht gespeichert. Kopierte Anhänge gehören zur neuen Agent-Sitzung. Kopiert wird ausschließlich manuell.

Bei geänderter Indexstruktur oder Leselogik wird der Index automatisch neu erstellt. Sitzungen erscheinen schrittweise; ursprüngliche Agent-Dateien und gespeicherte Einstellungen bleiben erhalten.

App und CLI nutzen denselben Datenordner: `~/Library/Application Support/koto` unter macOS, `%LOCALAPPDATA%\koto` unter Windows und `$XDG_DATA_HOME/koto` (Standard: `~/.local/share/koto`) unter Linux. `KOTO_HOME` oder `--data` überschreibt diesen Pfad.
