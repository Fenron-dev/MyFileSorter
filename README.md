# MyFileSorter

[![Tests](https://github.com/Fenron-dev/MyFileSorter/actions/workflows/test.yml/badge.svg)](https://github.com/Fenron-dev/MyFileSorter/actions/workflows/test.yml)
[![Desktop builds](https://github.com/Fenron-dev/MyFileSorter/actions/workflows/build.yml/badge.svg)](https://github.com/Fenron-dev/MyFileSorter/actions/workflows/build.yml)

MyFileSorter ist eine lokale Desktop-Anwendung, die unsortierte Hörbücher prüft und einen sicheren Zielplan für Audiobookshelf erstellt.

Der aktuelle Stand ist das erste ausführbare Inkrement:

- rekursiver, rein lokaler Scan unterstützter Audiodateien
- Auslesen eingebetteter Metadaten über ein optional vorhandenes `ffprobe`
- lokale Vorschläge aus Dateinamen und Tags
- ordnerbasierte Erkennung von Mustern wie `Autor - Serie 03 - Titel`
- Banddarstellung als `01 - Titel` und separates Info-Feld für Hinweise wie `Ungekürzt`
- sichtbarer aktueller Quellpfad pro Vorschlag
- explizite Nutzerbestätigung pro Hörbuch
- Checkbox-Auswahl: Nur als fertig markierte Hörbücher gelangen in den Importplan
- konfliktfreier Dry-Run-Operationsplan
- wahlweise fail-closed Behandlung vorhandener Ziele oder SHA-256-verifiziertes, Undo-fähiges Überspringen identischer Dateien
- ausdrücklich bestätigte, journalisierte Ausführung
- vollständige Quellenprüfung vor der ersten Dateioperation, inklusive sicherer Auflösung äquivalenter Unicode-Pfadschreibweisen
- Live-Fortschritt nach Operationen und Datenmenge einschließlich aktuell bearbeiteter Datei
- SHA-256-Prüfung jeder Kopie, bevor die Quelldatei entfernt wird
- Undo, solange die importierten Zieldateien unverändert sind
- optionales Einsortieren von E-Books unter `# Ebooks/Autor/Serie oder Buch`
- optionale, Undo-fähige Bereinigung von Cover-, NFO-, M3U- und CUE-Dateien
- persistentes JSONL-Sitzungslog mit einer direkt in der App erreichbaren Logansicht
- dauerhafter Importverlauf aus den Ausführungsjournalen, einschließlich Undo nach einem App-Neustart
- automatischer Abgleich von Arbeitsstand und Journalen nach Absturz oder fehlgeschlagenem Speichern
- native Ordnerablage per Drag & Drop als Alternative zur Ordnerauswahl
- getrennte feste oder automatische Stellenzahl für Band- und Tracknummern
- wahlweise einheitliche Audiodateinamen oder bereinigte vorhandene Kapitelbezeichnungen
- lokal gespeicherte Quell-/Zielpfade und Importoptionen für den nächsten App-Start
- bewusst ausgelöste Online-Suche über Audible Deutschland oder Google Books
- auswählbare Online-Treffer, die erneut vom Nutzer bestätigt werden müssen
- verwaltbare AI-Profile für Ollama, LM Studio, OpenAI, OpenRouter, Groq und OpenAI-kompatible Endpoints
- bewusst ausgelöste AI-Analyse ausschließlich mit Ordner-/Dateinamen und ausgewählten Textmetadaten
- prüfbare AI-Vorschläge mit optional anschließendem Audible-Abgleich
- schlanke Wails-Oberfläche ohne Frontend-Abhängigkeiten
- GitHub-Actions-Builds für macOS, Linux und Windows

Dateien werden erst nach Bestätigung des Zielplans und Aktivierung der Verschiebe-Checkbox übertragen. Der anschließende Button „Dateien verschieben“ startet den Vorgang direkt in der App, ohne einen möglicherweise unsichtbaren Systemdialog. Vor der ersten Änderung prüft die App sämtliche Quelldateien erneut. Nicht mehr vorhandene oder seit dem Scan veränderte Quellen blockieren damit den gesamten Lauf und können direkt über „Quelle neu scannen“ aktualisiert werden. Dazu schreibt die App zunächst eine temporäre Zieldatei, vergleicht die SHA-256-Prüfsumme und entfernt erst danach die Quelle. Verweigert ein Quelllaufwerk das Löschen, bleibt die geprüfte Kopie erhalten, die App setzt den Lauf fort und zeigt die betroffenen Quellen als Warnung an. Jeder Lauf wird im Benutzer-Konfigurationsordner journalisiert. Online- und AI-Anfragen finden ausschließlich nach einer bewussten Auswahl durch den Nutzer statt.

AI-Profile liegen im Benutzer-Konfigurationsordner. API-Schlüssel werden im nativen System-Schlüsselbund (macOS Keychain, Windows Credential Manager bzw. Linux Secret Service) abgelegt und weder an das Frontend zurückgegeben noch protokolliert. Bereits vorhandene AES-GCM-Profile werden beim Start sicher migriert; ohne verfügbaren Linux Secret Service bleiben sie weiterhin lesbar und die Migration wird später erneut versucht. Für Ollama und LM Studio ist üblicherweise kein Schlüssel erforderlich.

## Zielstruktur

```text
Mit Serie:
Autor/Serie/01 - Buchtitel/01 - Buchtitel.m4b

Ohne Serie:
Autor/Buchtitel/01 - Buchtitel.m4b

E-Books:
# Ebooks/Autor/Serie/01 - Buchtitel/Buchtitel.epub
```

## Entwicklung

Alle Builds sind für GitHub Actions vorgesehen. Lokal müssen für die Bearbeitung keine Abhängigkeiten installiert und keine Binärdateien erzeugt werden.

Die Workflows verwenden Go 1.26 und die festgelegte Wails-Version `v2.12.0`. Vor jedem Desktop-Build laufen Formatprüfung, `go vet`, Race-Tests und `govulncheck`. Anschließend entstehen gepackte Artefakte samt SHA-256-Prüfsummen für Linux x64, Windows x64 sowie macOS auf Apple Silicon und Intel. Ein Tag im Format `v*` erstellt nach erfolgreicher Matrix einen GitHub-Release-Entwurf; Betriebssystem-Signierung und Apple-Notarisierung können später über Repository-Secrets aktiviert werden.

`ffprobe` ist für den ersten Stand optional. Ist es auf dem Zielsystem vorhanden, liest die App damit ID3-/MP4-Metadaten; andernfalls arbeitet sie ausschließlich mit lokalen Datei- und Ordnernamen weiter.

Weitere Projektinformationen:

- [Architektur- und Produktkonzept](docs/CONCEPT.md)
- [Build- und Release-Prozess](docs/RELEASE.md)
- [Beitragsrichtlinien](CONTRIBUTING.md)
- [Sicherheitsrichtlinie und vertrauliche Meldungen](SECURITY.md)

Für das Repository wurde noch keine Lizenz festgelegt. Diese Entscheidung bleibt ausdrücklich offen.
