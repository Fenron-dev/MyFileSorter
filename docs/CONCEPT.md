# Produkt- und Architekturkonzept

## Produktgrundsatz

MyFileSorter arbeitet standardmäßig vollständig lokal. Ein Scan löst weder Netzwerk- noch LLM-Anfragen aus. Online-Metadaten und AI sind bewusst ausgelöste Eskalationsstufen. Keine Dateioperation wird ohne einen bestätigten Vorschlag geplant.

## Erkennungspipeline

1. Unterstützte Audiodateien rekursiv erfassen.
2. Dateinamen, Pfade und eingebettete Metadaten lesen.
3. Dateien konservativ zu Hörbüchern gruppieren.
4. Den Hörbuchordner gegenüber generischen Tracknamen wie „Opening Credits“ oder „Kapitel 1“ priorisieren.
5. Strukturierte Ordnernamen wie `Autor - Serie 03 - Titel` lokal zerlegen.
6. Nachgestellte Bandnummern in die Serien-/Bandfelder verschieben und Editionshinweise wie „Ungekürzt“ separat halten.
7. Einen lokalen Metadatenvorschlag mit Herkunft, Quellpfad und Konfidenz erzeugen.
8. Den Vorschlag durch den Nutzer bestätigen oder bearbeiten lassen.
9. Bei Bedarf Audible/Google Books manuell abfragen.
10. Bei Bedarf später AI zur strukturierten Erkennung und Suchanfragebildung verwenden.
11. Nur bestätigte Vorschläge in den Operationsplan aufnehmen.
12. Vor der Ausführung Quelle, Ziel und Kollisionen erneut prüfen.
13. Nach ausdrücklicher Bestätigung journalisiert kopieren, per SHA-256 prüfen und erst dann die Quelle entfernen.

## Statusmodell

- `review_required`: lokaler Vorschlag liegt vor und muss geprüft werden
- `confirmed`: vom Nutzer bestätigt und planbar
- `excluded`: bewusst von der Verarbeitung ausgeschlossen
- `conflict`: Zielpfad oder Gruppierung ist nicht eindeutig
- `error`: Scan oder Metadatenerkennung ist fehlgeschlagen

Die Checkbox eines Vorschlags bildet den Status `confirmed` direkt ab. Nur angehakte Vorschläge werden geplant; alle übrigen Dateien bleiben unverändert im Quellordner.

## Diagnoseprotokoll

Jeder App-Start erzeugt eine eigene JSONL-Logdatei im Benutzer-Konfigurationsordner. Das aktuelle Sitzungslog ist in der App einsehbar und enthält Zeit, Stufe, Komponente, Ereignis und ausgewählte strukturierte Details. Protokolliert werden insbesondere Scan, Reviewstatus, Onlineabgleich, Planung, Import, Undo, Fehler und die zugehörige Journal-ID. Audiodaten, Zugangsdaten und API-Geheimnisse werden nicht protokolliert.

Unabhängig davon bildet die App aus den persistenten Ausführungsjournalen einen Importverlauf. Er zeigt frühere Läufe auch nach einem Neustart mit Ziel, Status und Umfang an. Ein Undo wird nur angeboten, wenn das Journal noch rückführbare Dateioperationen enthält.

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

- Serien- und Tracknummern sind unabhängig konfigurierbar: ohne Auffüllung, mit fester Stellenzahl oder automatisch anhand der höchsten erkannten Nummer. Der Dry Run zeigt die resultierenden Namen vor dem Import.
- Audiodateien können einheitlich nach dem Buchtitel benannt werden. Alternativ bleibt der vorhandene Kapitelname erhalten; nur Zahlenpräfix sowie Punkt- und Unterstrich-Trennungen werden portabel normalisiert.
- Dezimale Seriennummern bleiben erhalten.
- Namen werden für Windows, macOS und Linux normalisiert.
- Bestehende Zieldateien werden niemals überschrieben.
- E-Books werden optional parallel unter `# Ebooks` mit derselben Autor-/Serienstruktur abgelegt.
- Coverbilder, NFO-, M3U- und CUE-Dateien werden nur nach aktivierter Bereinigung aus dem Quellordner entfernt und für Undo journalisiert aufbewahrt.

## Spätere Eskalationsstufen

### Online suchen

Der Nutzer startet die Suche explizit. Audible ist für konkrete Hörbuchausgaben die Primärquelle; Google Books ergänzt Buchdaten. Treffer werden als Alternativen angezeigt und nie automatisch übernommen.

Umgesetzt sind Audible Deutschland und Google Books. Audible verwendet denselben gekapselten Katalog-/Detailansatz wie Audiobookshelf: regionale Audible-Suche und Detailauflösung über Audnexus. Anbieter, Region und Treffer bleiben vom restlichen Kern entkoppelt. Netzwerkaufrufe haben feste Zeit- und Größenlimits.

### Mit AI analysieren

Das LLM erhält nur Dateinamen und ausgewählte Metadaten, niemals Audiodaten. Es liefert strukturiert vermutete Felder und Suchanfragen. Ein anschließender Katalogabgleich und die Nutzerbestätigung bleiben erforderlich.

Umgesetzt sind Profile für Ollama, LM Studio, OpenAI, OpenRouter, Groq und benutzerdefinierte OpenAI-kompatible Endpoints. Profile enthalten Anbieter, Endpoint und Modell; optionale API-Schlüssel werden lokal AES-GCM-verschlüsselt gespeichert und nie über die Profil-API oder das Sitzungslog ausgegeben. Die AI-Analyse übermittelt nur den Namen des Hörbuchordners, die Dateinamen, den lokalen Vorschlag und ausgewählte eingebettete Metadaten. Vollständige Pfade und Audiodaten bleiben lokal.

## Sichere Ausführung

- Dry Run ist der Standard.
- Nach der geprüften Vorschau ist die sichtbare Checkbox die ausdrückliche Ausführungsfreigabe; der Verschiebe-Button verwendet exakt den Zielpfad und die Optionen dieser Vorschau.
- Die Datei wird unabhängig vom Dateisystem zunächst temporär kopiert.
- Quelle und Kopie werden per SHA-256 verglichen, die Kopie atomar finalisiert und erst danach die Quelle entfernt.
- Jede Operation wird journalisiert.
- Die Desktop-Oberfläche erhält nach jeder abgeschlossenen Operation einen Fortschrittsstand mit Datei- und Bytezähler.
- Undo ist erlaubt, solange das Ziel nicht nachträglich verändert wurde.
- Konflikte und unsichere Vorschläge blockieren die automatische Ausführung.

## Technischer Aufbau

- Go: Scanner, Metadaten, Naming, Operationsplan und später Dateioperationen
- Wails v2: plattformübergreifende Desktop-Hülle
- Dependency-freies HTML/CSS/JavaScript: erste Review-Oberfläche
- GitHub Actions: Tests und native Builds auf den drei Zielplattformen
- Provider-Schnittstellen: spätere Audible-, Google-Books- und LLM-Adapter
