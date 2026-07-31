# Sicherheitsrichtlinie

## Unterstützte Versionen

MyFileSorter befindet sich noch in einer frühen Entwicklungsphase. Sicherheitskorrekturen werden für den aktuellen Stand von `main` und, soweit praktikabel, für die neueste veröffentlichte Version bereitgestellt. Ältere Artefakte erhalten derzeit keine garantierten Backports.

## Sicherheitslücken vertraulich melden

Bitte veröffentliche vermutete Sicherheitslücken nicht als normales GitHub-Issue und füge dort insbesondere keine Exploits, API-Schlüssel, privaten Dateipfade oder Logdateien an.

Bevorzugt wird eine private Meldung über **Security → Advisories → Report a vulnerability** im GitHub-Repository. Sollte die private Meldung dort nicht verfügbar sein, darf ein öffentliches Issue ohne technische Details eröffnet werden, um einen privaten Kontaktweg anzufragen.

Eine hilfreiche Meldung enthält:

- betroffene Version beziehungsweise Commit-ID und Betriebssystem,
- nachvollziehbare, möglichst minimale Reproduktionsschritte,
- erwartetes und tatsächliches Verhalten,
- mögliche Auswirkungen auf lokale Dateien, Zugangsdaten oder externe Dienste,
- eine Einschätzung, ob die Lücke bereits aktiv ausgenutzt wird.

Wir versuchen, den Eingang innerhalb von sieben Tagen zu bestätigen und anschließend Schweregrad, Behebung und Veröffentlichung abzustimmen. Diese Zeitangaben sind Zielwerte und keine Garantie.

## Besonders sensible Bereiche

MyFileSorter verarbeitet vom Nutzer ausgewählte Verzeichnisse und kann bestätigte Dateien kopieren, prüfen und anschließend aus der Quelle entfernen. Besonders relevant sind deshalb:

- Umgehung der Quell- oder Zielverzeichnisgrenzen, Pfad-Traversal und Symlink-Angriffe,
- Datenverlust, unvollständige Übertragungen oder unzuverlässiges Undo,
- Offenlegung lokaler Pfade, Metadaten, Logs oder API-Zugangsdaten,
- unbeabsichtigte Online- beziehungsweise AI-Anfragen,
- Manipulation von Release-Artefakten, Abhängigkeiten oder GitHub-Actions.

## Datenschutz und lokale Daten

Online- und AI-Abgleiche sollen ausschließlich nach einer bewussten Nutzeraktion stattfinden. Vor einer Sicherheitsmeldung sollten Logausschnitte auf persönliche Dateinamen, lokale Pfade, Buchmetadaten und Tokens geprüft und entsprechend gekürzt oder anonymisiert werden.

API-Schlüssel werden im nativen System-Schlüsselbund gespeichert und nicht an das Frontend zurückgegeben. Ältere lokal verschlüsselte Profile werden transaktional migriert, sobald der Schlüsselbund verfügbar ist. Das schützt nicht vor einem bereits kompromittierten Benutzerkonto oder Betriebssystem. Nutzer sollten Zugangsdaten mit möglichst kleinen Berechtigungen verwenden und sie bei einem Verdacht widerrufen.

## Release-Vertrauen

Offizielle Testartefakte und Releases werden ausschließlich durch GitHub Actions erstellt. Jedes Paket erhält eine SHA-256-Prüfsumme; Tag-Builds werden zunächst als Entwurf veröffentlicht. Details zur Prüfung und zu optionaler Betriebssystem-Signierung stehen in [docs/RELEASE.md](docs/RELEASE.md).
