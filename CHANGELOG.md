# Changelog

## v0.2.0-dev

- PCM-Cache, Vorladen und zugehörige Config/Status-Felder entfernt; MPEG-2.5-fähiger Decoder bleibt erhalten.
- Werbetexte aus der UI entfernt.
- Meldungshistorie und letzten Fehler über UI oder `DELETE /api/history` leeren.
- MPD-Anbindung nur bei ausdrücklich gesetzter Adresse aktiv.
- Beispielstream heißt `notifications`; GitHub-Web-Release baut weiterhin Multiarch-Images und Linux-Binaries.

## v0.1.1-dev

- Lokaler PCM-Cache standardmäßig aktiv, mit Vorladen beim Start, Inhaltsprüfung und konfigurierbarem LRU-Speicherlimit.
- MP3-Decoder ersetzt: MPEG-2.5 wird jetzt ebenso wie MPEG-1 und MPEG-2 unterstützt; weiterhin ohne FFmpeg und CGO.
- Cache-Verdrängung lässt wartende und laufende Sounds unverändert verfügbar.
- Cache-Statistiken im Status-Endpunkt ergänzt.
- Config-Bind-Mount erzeugt bei fehlender Datei kein Verzeichnis mehr; temporären Docker-Speicher für den Cache auf 1 GiB erhöht.
