# Produkt- und Architekturkonzept

## Produktgrundsatz

MyFileSorter arbeitet standardmäßig vollständig lokal. Ein Scan löst weder Netzwerk- noch LLM-Anfragen aus. Online-Metadaten und AI sind bewusst ausgelöste Eskalationsstufen. Keine Dateioperation wird ohne einen bestätigten Vorschlag geplant.

## Erkennungspipeline

1. Unterstützte Audiodateien rekursiv erfassen.
2. Dateinamen, Pfade und eingebettete Metadaten lesen.
3. Dateien konservativ zu Hörbüchern gruppieren.
4. Einen lokalen Metadatenvorschlag mit Herkunft und Konfidenz erzeugen.
5. Den Vorschlag durch den Nutzer bestätigen oder bearbeiten lassen.
6. Bei Bedarf später Audible/Google Books manuell abfragen.
7. Bei Bedarf später AI zur strukturierten Erkennung und Suchanfragebildung verwenden.
8. Nur bestätigte Vorschläge in den Operationsplan aufnehmen.
9. Vor der späteren Ausführung Quelle, Ziel, Kollisionen und verfügbaren Speicher prüfen.

## Statusmodell

- `review_required`: lokaler Vorschlag liegt vor und muss geprüft werden
- `confirmed`: vom Nutzer bestätigt und planbar
- `excluded`: bewusst von der Verarbeitung ausgeschlossen
- `conflict`: Zielpfad oder Gruppierung ist nicht eindeutig
- `error`: Scan oder Metadatenerkennung ist fehlgeschlagen

## Metadatenquellen

Lokale Quellen haben eine nachvollziehbare Priorität:

1. ASIN/ISBN und explizite Tags
2. Album- und Autor-Tags
3. Serien- und Track-Tags
4. Dateiname
5. Ordnername

`ffprobe` ist ein optionaler Adapter. Fehlt es, funktioniert der Scan weiterhin nur mit Pfad- und Dateinamenerkennung. Die Kernarchitektur hängt nicht von diesem Werkzeug ab.

## Gruppierung

Ein Ordner, der direkt Audiodateien enthält, gilt zunächst als ein Hörbuch mit mehreren Tracks. Lose Audiodateien direkt im gewählten Quellordner werden konservativ als einzelne Hörbücher behandelt. Mehrdeutige Sammlungen werden später durch zusätzliche Tag-Evidenz aufgeteilt; niemals allein aufgrund einer LLM-Vermutung.

## Naming

```text
Serie:      Autor/Serie/NN - Titel/TT - Titel.ext
Einzelbuch: Autor/Titel/TT - Titel.ext
```

- Serien- und Tracknummern sind mindestens zweistellig.
- Dezimale Seriennummern bleiben erhalten.
- Namen werden für Windows, macOS und Linux normalisiert.
- Bestehende Zieldateien werden niemals überschrieben.

## Spätere Eskalationsstufen

### Online suchen

Der Nutzer startet die Suche explizit. Audible ist für konkrete Hörbuchausgaben die Primärquelle; Google Books ergänzt Buchdaten. Treffer werden als Alternativen angezeigt und nie automatisch übernommen.

Umgesetzt sind Audible Deutschland und Google Books. Audible verwendet denselben gekapselten Katalog-/Detailansatz wie Audiobookshelf: regionale Audible-Suche und Detailauflösung über Audnexus. Anbieter, Region und Treffer bleiben vom restlichen Kern entkoppelt. Netzwerkaufrufe haben feste Zeit- und Größenlimits.

### Mit AI analysieren

Das LLM erhält nur Dateinamen und ausgewählte Metadaten, niemals Audiodaten. Es liefert strukturiert vermutete Felder und Suchanfragen. Ein anschließender Katalogabgleich und die Nutzerbestätigung bleiben erforderlich.

## Sichere Ausführung (Folgeinkrement)

- Dry Run ist der Standard.
- Gleiches Dateisystem: atomare Umbenennung, soweit möglich.
- Dateisystemgrenze: kopieren, Prüfsumme vergleichen, finalisieren, Quelle entfernen.
- Jede Operation wird journalisiert.
- Undo ist erlaubt, solange das Ziel nicht nachträglich verändert wurde.
- Konflikte und unsichere Vorschläge blockieren die automatische Ausführung.

## Technischer Aufbau

- Go: Scanner, Metadaten, Naming, Operationsplan und später Dateioperationen
- Wails v2: plattformübergreifende Desktop-Hülle
- Dependency-freies HTML/CSS/JavaScript: erste Review-Oberfläche
- GitHub Actions: Tests und native Builds auf den drei Zielplattformen
- Provider-Schnittstellen: spätere Audible-, Google-Books- und LLM-Adapter
