# MyFileSorter

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
- ausdrücklich bestätigte, journalisierte Ausführung
- SHA-256-Prüfung jeder Kopie, bevor die Quelldatei entfernt wird
- Undo, solange die importierten Zieldateien unverändert sind
- optionales Einsortieren von E-Books unter `# Ebooks/Autor/Serie oder Buch`
- optionale, Undo-fähige Bereinigung von Cover-, NFO-, M3U- und CUE-Dateien
- persistentes JSONL-Sitzungslog mit einer direkt in der App erreichbaren Logansicht
- native Ordnerablage per Drag & Drop als Alternative zur Ordnerauswahl
- getrennte feste oder automatische Stellenzahl für Band- und Tracknummern
- wahlweise einheitliche Audiodateinamen oder bereinigte vorhandene Kapitelbezeichnungen
- bewusst ausgelöste Online-Suche über Audible Deutschland oder Google Books
- auswählbare Online-Treffer, die erneut vom Nutzer bestätigt werden müssen
- schlanke Wails-Oberfläche ohne Frontend-Abhängigkeiten
- GitHub-Actions-Builds für macOS, Linux und Windows

Dateien werden erst nach Bestätigung des Zielplans verschoben. Dazu schreibt die App zunächst eine temporäre Zieldatei, vergleicht die SHA-256-Prüfsumme und entfernt erst danach die Quelle. Jeder Lauf wird im Benutzer-Konfigurationsordner journalisiert. Online-Anfragen finden ausschließlich nach Auswahl eines Anbieters durch den Nutzer statt.

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

Die Workflows werden aktiv, sobald dieses Repository mit einem GitHub-Remote verbunden und gepusht wurde. Der Build verwendet die stabile Wails-Version `v2.12.0` und erzeugt Artefakte für Linux x64, Windows x64 sowie macOS auf Apple Silicon und Intel.

`ffprobe` ist für den ersten Stand optional. Ist es auf dem Zielsystem vorhanden, liest die App damit ID3-/MP4-Metadaten; andernfalls arbeitet sie ausschließlich mit lokalen Datei- und Ordnernamen weiter.

Die Architektur- und Produktentscheidungen stehen in [docs/CONCEPT.md](docs/CONCEPT.md).
