# Zu MyFileSorter beitragen

Danke für das Interesse an MyFileSorter. Kleine, klar abgegrenzte Änderungen sind besonders willkommen, weil die App mit echten Mediendateien und potenziell destruktiven Dateioperationen arbeitet.

> **Hinweis zur Lizenz:** Für das Repository wurde noch keine Lizenz festgelegt. Bitte kläre umfangreiche Beiträge vorab in einem Issue. Eine Lizenz wird durch diesen Leitfaden ausdrücklich nicht eingeräumt oder vorweggenommen.

## Vor einer Änderung

- Sicherheitsprobleme bitte gemäß [SECURITY.md](SECURITY.md) vertraulich melden.
- Für größere Features, neue Online-Anbieter oder Änderungen am Dateiplan zuerst ein Issue mit Anwendungsfall und gewünschtem Verhalten eröffnen.
- Keine realen API-Schlüssel, privaten Bibliothekspfade, Hörbuch-Metadaten oder vollständigen Nutzerlogs committen.
- Änderungen an Verschieben, Löschen, Journal und Undo benötigen passende Fehler- und Wiederanlauftests.

## Entwicklungsmodell

Das Projekt verwendet Go mit Wails und ein schlankes Frontend ohne Paketmanager. Der verbindliche Toolchain-Stand in GitHub Actions ist Go 1.26; die Wails-CLI ist auf `v2.12.0` festgelegt.

Lokale Builds sind für Beiträge nicht erforderlich. Die GitHub-Actions-Pipeline übernimmt Formatprüfung, `go vet`, Race-Tests, `govulncheck`, plattformspezifische Builds und Paketierung. Wer lokal prüfen kann, darf vor einem Push dieselben nicht destruktiven Prüfungen ausführen:

```bash
gofmt -w <geänderte-go-dateien>
go vet ./...
go test -race -count=1 ./...
node --check frontend/dist/app.js
```

## Pull Requests

Ein Pull Request sollte:

- ein Problem oder Feature pro Änderung behandeln,
- das Nutzerverhalten und Sicherheitsauswirkungen kurz beschreiben,
- neue oder geänderte Logik durch Tests abdecken,
- keine generierten Binärdateien, Zertifikate oder lokale Journale enthalten,
- alle verpflichtenden GitHub-Actions-Prüfungen bestehen.

Änderungen an Release-Workflows benötigen eine Erklärung zu Berechtigungen, verwendeten Drittanbieter-Actions und benötigten Secrets. Actions werden nach Möglichkeit auf vollständige Commit-SHAs fixiert und über Dependabot aktualisiert.

## Dateisicherheit

Code, der Dateien verändert, muss weiterhin folgende Grundsätze einhalten:

1. Vorschau und tatsächliche Ausführung verwenden denselben validierten Plan.
2. Keine Operation darf unbemerkt außerhalb der bestätigten Quell- und Zielwurzeln schreiben oder löschen.
3. Quellen werden erst nach erfolgreicher Kopie und Integritätsprüfung entfernt.
4. Teilerfolge und Fehler bleiben im Journal nachvollziehbar und dürfen ein späteres Undo nicht verfälschen.
5. Online-Kommunikation bleibt opt-in und darf keine unnötigen lokalen Daten übertragen.

## Dokumentation

Nutzerrelevante Änderungen gehören zusätzlich in die README oder die passende Datei unter `docs/`. Der Release-Prozess ist in [docs/RELEASE.md](docs/RELEASE.md) beschrieben.
