# Build- und Release-Prozess

MyFileSorter wird ausschließlich in GitHub Actions für Linux, Windows und macOS gebaut. Dadurch sind lokale Kompilierungen für die Projektpflege nicht erforderlich und alle verteilten Pakete stammen aus einer nachvollziehbaren Pipeline.

## Verbindliche Prüfungen

Der wiederverwendbare Workflow `.github/workflows/quality.yml` läuft sowohl im Test- als auch im Desktop-Build-Workflow. Bevor ein plattformspezifischer Build startet, müssen erfolgreich sein:

1. Download und Prüfen der Go-Module,
2. `gofmt`-Kontrolle,
3. `go vet ./...`,
4. `go test -race -count=1 ./...`,
5. JavaScript-Syntaxprüfung,
6. `govulncheck ./...`.

Die Pipeline verwendet Go 1.26, einen Go-Modul-Cache und die festgelegte Wails-CLI `v2.12.0`. GitHub-Actions sind auf vollständige Commit-SHAs fixiert; Dependabot schlägt Aktualisierungen für Go-Module und Actions vor.

## Testartefakte

Pushes auf `main` und `codex/**` sowie manuell ausgelöste Desktop-Builds erzeugen:

| Plattform | Paket |
| --- | --- |
| Linux x64 | `MyFileSorter-linux-x64.tar.gz` |
| Windows x64 | `MyFileSorter-windows-x64.zip` |
| macOS Apple Silicon | `MyFileSorter-macos-arm64.tar.gz` |
| macOS Intel | `MyFileSorter-macos-x64.tar.gz` |

Zu jedem Paket gehört eine gleichnamige `.sha256`-Datei. Normale Workflow-Artefakte werden 14 Tage aufbewahrt.

## GitHub-Release erstellen

1. Sicherstellen, dass der gewünschte Commit auf `main` liegt und die Qualitätsprüfungen bestanden hat.
2. Einen semantischen Tag wie `v0.2.0` auf diesen Commit setzen und pushen.
3. Der Desktop-Workflow prüft den Code erneut und baut alle vier Plattformpakete.
4. Nach erfolgreichen Builds erstellt oder aktualisiert er einen **Entwurf** des GitHub-Releases. Zusätzlich zu den Einzelprüfsummen enthält dieser `SHA256SUMS`.
5. Pakete, Release Notes und gegebenenfalls Signaturstatus manuell prüfen; erst danach den Entwurf veröffentlichen.

Ein fehlgeschlagenes Quality-Gate oder ein fehlender Matrix-Build verhindert die Release-Erstellung.

## Optionale Windows-Signierung

Die Windows-EXE wird bei Tag-Builds signiert, wenn beide Repository-Secrets vorhanden sind:

| Secret | Inhalt |
| --- | --- |
| `WINDOWS_CERTIFICATE` | Base64-kodierte PFX-Datei |
| `WINDOWS_CERTIFICATE_PASSWORD` | Passwort der PFX-Datei |

Die Pipeline nutzt SHA-256 und einen öffentlichen Zeitstempeldienst. Fehlt eines der Secrets, bleibt das Paket bewusst unsigniert und der Build läuft weiter.

## Optionale macOS-Signierung und Notarisierung

Für die Codesignatur werden benötigt:

| Secret | Inhalt |
| --- | --- |
| `MACOS_CERTIFICATE` | Base64-kodierte Developer-ID-P12-Datei |
| `MACOS_CERTIFICATE_PASSWORD` | Passwort der P12-Datei |
| `MACOS_SIGNING_IDENTITY` | Vollständiger Name der Developer-ID-Identität |

Für die anschließende Apple-Notarisierung werden zusätzlich benötigt:

| Secret | Inhalt |
| --- | --- |
| `APPLE_ID` | Apple-ID des Entwicklerkontos |
| `APPLE_TEAM_ID` | Apple Developer Team-ID |
| `APPLE_APP_PASSWORD` | App-spezifisches Passwort für `notarytool` |

Die P12-Datei wird nur im temporären Runner-Schlüsselbund importiert. Nach erfolgreicher Notarisierung wird das Ticket an das App-Bundle angeheftet und geprüft. Fehlen Signier- oder Apple-Secrets, werden die entsprechenden Schritte übersprungen; es werden keine Platzhalter-Zugangsdaten verwendet.

## Prüfsummen verifizieren

Linux:

```bash
sha256sum --check MyFileSorter-linux-x64.tar.gz.sha256
```

macOS:

```bash
shasum -a 256 --check MyFileSorter-macos-arm64.tar.gz.sha256
```

Windows PowerShell:

```powershell
(Get-FileHash .\MyFileSorter-windows-x64.zip -Algorithm SHA256).Hash
Get-Content .\MyFileSorter-windows-x64.zip.sha256
```

Der ausgegebene Hash muss exakt mit dem ersten Wert der Prüfsummendatei übereinstimmen.

## Berechtigungen und Secrets

Workflows besitzen standardmäßig nur lesenden Repository-Zugriff. Ausschließlich der Tag-Release-Job erhält `contents: write`, um den Release-Entwurf und seine Dateien anzulegen. Fork-basierte Pull Requests erhalten keine Repository-Secrets. Zertifikate und Passwörter dürfen weder in Workflow-Dateien noch in Logs oder Artefakten landen.
