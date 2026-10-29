# snapnotify

Kleiner Notification-Service für **Snapcast, optional mit MPD-Anbindung**, mit Web-UI, HTTP und optional MQTT. Unterstützt MP3, PCM-WAV und Ogg Vorbis. Die Decoder laufen in Go; kein FFmpeg, ALSA, CGO oder Audio-Gerät im Container erforderlich.

## Verhalten

- Eine Notification erreicht alle zu Beginn des Wiedergabeblocks verbundenen, nicht stummgeschalteten Clients. Stummgeschaltete Gruppen und Clients bleiben stumm. Während eines Blocks neu verbundene Clients werden nicht nachträglich übernommen.
- Dateien werden vor dem Einreihen vorbereitet und auf **48 kHz, 16 Bit, Stereo, Little Endian** gebracht. Lokale Sounds, URLs und Uploads werden jeweils vollständig decodiert; es gibt keinen PCM-Cache. Ungültige Dateien unterbrechen keine Musik.
- Eine FIFO-Warteschlange, ohne Prioritäten, Wiederholungen oder Persistenz. Ihre Reihenfolge entspricht dem erfolgreichen Abschluss der Vorbereitung; ein langsamer Download kann von einer später gestarteten kleinen Datei überholt werden.
- Vor einem Wiedergabeblock: MPD bei laufender Wiedergabe pausieren, Gruppen auf `notifications` schalten, alte Audiopuffer bei bisheriger Lautstärke leeren, Notification-Lautstärke setzen.
- Mehrere bereits wartende Notifications laufen im selben Block. Danach werden die Audio-Puffer durch Stille geleert und ursprüngliche Client-Lautstärken sowie Stream-Zuordnungen wiederhergestellt. MPD wird nur fortgesetzt, wenn es von snapnotify pausiert wurde und weiterhin am erwarteten Titel/Abspielpunkt pausiert ist.
- Sichtbare manuelle Änderungen an Client-Lautstärke, Mute und Gruppenzuordnung werden bei der Wiederherstellung respektiert. Eine Änderung zurück auf exakt denselben Wert kann nicht von einem unveränderten Zustand unterschieden werden.
- HTTP `202` bedeutet **vorbereitet und eingereiht**, nicht erfolgreich abgespielt. Wiedergabefehler stehen im Log und in `/api/status` sowie der Web-UI. Die letzten 20 Ergebnisse und der letzte Fehler bleiben bis zum Neustart oder bis zum Leeren über die UI/API sichtbar.
- Temporäre Job-Dateien werden nach Wiedergabe/Fehler gelöscht. SIGTERM/SIGINT bricht die Wiedergabe ab und versucht, den vorherigen Zustand wiederherzustellen. Bei hartem Prozessabbruch oder unerreichbaren Diensten ist eine Wiederherstellung nicht garantiert; der Zustand wird nicht auf Platte gesichert.

## Docker starten

1. `config.example.yaml` nach `config.yaml` kopieren. Adressen und Lautstärke anpassen.
2. Lokale Sounds in `sounds/` legen und unter `sounds:` IDs zuordnen. Pfade sind relativ zu `audio.directory`; dieses Verzeichnis ist relativ zur Config-Datei. Änderungen an der YAML werden beim Neustart übernommen.
3. Den `[stream]`-Abschnitt von Snapserver gemäß `snapserver.stream.conf` anpassen und Snapserver neu starten. Die MPD-FIFO-Konfiguration bleibt unverändert.
4. Snapnotify und Snapserver mit demselben Docker-Netzwerk verbinden; MPD nur bei aktivierter MPD-Anbindung ebenfalls verbinden. Die Compose-Datei erwartet ein bereits bestehendes Netzwerk namens `audio`; dessen Namen über `AUDIO_NETWORK` anpassen. Snapserver muss darin als `snapserver` erreichbar sein oder die Config entsprechend ändern. MPD wird nur verwendet, wenn `mpd.address` ausdrücklich gesetzt ist, beispielsweise auf `mpd:6600`.
5. Starten:

```sh
cp config.example.yaml config.yaml
# config.yaml bearbeiten und Snapserver entsprechend konfigurieren
AUDIO_NETWORK=dein_bestehendes_netzwerk docker compose up -d --build
docker compose logs -f snapnotify
```

Die UI ist unter **http://localhost:8080** erreichbar. Compose bindet absichtlich an `127.0.0.1`; für den Zugriff aus dem LAN in `compose.yaml` die linke Adresse auf die LAN-IP des Hosts setzen oder einen internen Reverse Proxy verwenden. Der Dienst hat keine Anmeldung und ist für ein vertrauenswürdiges internes Netz gedacht. URLs dürfen auch interne HTTP-/HTTPS-Server erreichen. Es gibt keine CORS-Freigabe; fremde Browser-Origins werden für Schreibzugriffe abgewiesen.

Benötigte Verbindungen innerhalb des Docker-Netzwerks:

| Ziel | Port | Zweck |
|---|---:|---|
| Snapserver | 1705/TCP | JSON-RPC-Steuerung |
| Snapserver | 4953/TCP | Notification-Audio an `notifications` |
| MPD, optional | 6600/TCP | Status, Pause, Fortsetzen bei gesetzter Adresse |
| MQTT-Broker, optional | 1883/TCP bzw. 8883/TCP | Trigger |

Snapservers TCP-Steuerung im Abschnitt `[tcp]` muss aktiviert und für snapnotify erreichbar sein (`enabled = true`, `bind_to_address = 0.0.0.0`, `port = 1705`). Diese internen Ports müssen nicht am Docker-Host veröffentlicht werden. Audio- und Steueradresse müssen zum selben Snapserver gehören. `notifications` gehört exklusiv snapnotify; den bisherigen Test-Sender nicht parallel betreiben. Nur eine snapnotify-Instanz pro Snapserver verwenden.

Dein bisheriger Aufbau bleibt erhalten:

```ini
source = pipe:///tmp/snapfifo?name=mpd&codec=null
source = tcp://0.0.0.0:4953?name=notifications&codec=flac&sampleformat=48000:16:2
source = meta:///notifications/mpd?name=Mixed&codec=flac
```

Wesentliche Änderung: `notifications` bekommt **`codec=flac`** statt `null`, damit Gruppen direkt auf den Stream umgeschaltet werden können. `Mixed` darf weiterhin `notifications` vor `mpd` priorisieren. Der Service schaltet die hörbaren Gruppen für die Notification explizit auf `notifications` und anschließend wieder auf ihren vorherigen Stream.

Gemountete Dateien müssen für UID/GID `65532:65532` lesbar sein; üblicherweise Verzeichnisse `755`, Audiodateien `644`. Für Config-Dateien mit Kennwörtern geeignete Gruppenrechte verwenden. MQTT- und MPD-Kennwörter können alternativ über `SNAPNOTIFY_MQTT_PASSWORD` und `SNAPNOTIFY_MPD_PASSWORD` gesetzt werden. Bei MQTT-TLS (`ssl://…`) werden Zertifikate gegen die System-CA geprüft; private CAs im Image/Container ergänzen.

## Optionale MPD-Anbindung

Ohne `mpd:`-Abschnitt oder mit `mpd.address: ""` wird keine MPD-Verbindung aufgebaut. Notifications funktionieren ausschließlich über Snapcast; die ursprünglichen Stream-Zuordnungen und Lautstärken werden weiterhin wiederhergestellt. Andere Audioquellen laufen währenddessen im Hintergrund weiter.

Soll snapnotify MPD pausieren und anschließend fortsetzen, ausdrücklich konfigurieren:

```yaml
mpd:
  address: mpd:6600
  password: ""
```

Ist eine Adresse gesetzt, aber MPD nicht erreichbar, wird die Notification mit einem Fehler abgebrochen. Es gibt dann keinen stillen Fallback auf Wiedergabe ohne Pause.

Ohne MPD genügt auf Snapserver die Notification-Quelle:

```ini
[stream]
default_source = notifications
source = tcp://0.0.0.0:4953?name=notifications&codec=flac&sampleformat=48000:16:2
```

## Aktualisierung von v0.1.1-dev

- Einen vorhandenen `cache:`-Abschnitt aus der Config entfernen; Cache, Vorladen und Cache-Statistiken sind vollständig entfernt.
- Die neue Beispielkonfiguration nutzt `notifications` als Streamname. Bestehendes `snapcast.stream: c1` funktioniert weiterhin. Wenn du umbenennst, sowohl die Snapserver-Quelle samt Meta-Verweis als auch `snapcast.stream` in snapnotify ändern.
- MPD ist ohne Adresse standardmäßig deaktiviert. Eine bereits ausdrücklich gesetzte Adresse bleibt wirksam.
- Eigene Config, Sounds und Compose-Anpassungen behalten. Danach neu bauen und starten.

## Web-UI

- **Sounds:** konfigurierte IDs mit Dateiname und Abspielen-Button.
- **URL:** direkte HTTP-/HTTPS-Datei herunterladen und einreihen.
- **Upload:** genau eine Datei auswählen/ablegen und einmalig abspielen. Kein dauerhaftes Hinzufügen zur Konfiguration.
- **Lautstärke:** globaler Wert `0..100`, gilt ab der nächsten Notification, auch innerhalb einer Warteschlange. Änderungen während eines laufenden Sounds verändern diesen nicht. Bei einem Wechsel zwischen Sounds werden zuerst die alten Puffer geleert.
- Status und Warteschlangenlänge werden etwa sekündlich aktualisiert.
- **Letzte Meldungen → Leeren:** löscht die Historie und den letzten Fehler. Laufende Wiedergabe und Warteschlange bleiben erhalten; später abgeschlossene Jobs erscheinen wieder als neue Meldung.

Die über UI/API eingestellte Lautstärke ist **flüchtig**. Nach einem Neustart gilt wieder `audio.volume` aus der YAML. Bei unterschiedlicher Hardware ergibt derselbe Prozentwert nicht zwingend dieselbe wahrgenommene Lautstärke.

## HTTP-API

### Konfigurierte Datei

```sh
curl -X POST http://localhost:8080/play/doorbell
```

### URL

```sh
curl -X POST http://localhost:8080/play-url \
  -H 'Content-Type: application/json' \
  -d '{"url":"http://homeassistant:8123/local/gong.mp3"}'
```

Nur HTTP/HTTPS, keine Zugangsdaten in der URL, maximal fünf Weiterleitungen. Der Download ist endlich und vollständig; keine Radio-/HLS-Streams, Playlists, Cookies oder eigenen Auth-Header. Die URL-Abfrageparameter werden nicht im Jobnamen angezeigt.

### Datei-Upload

```sh
curl -X POST http://localhost:8080/play-file -F 'file=@./gong.wav'
```

### Notification-Lautstärke

```sh
curl http://localhost:8080/api/volume
curl -X PUT http://localhost:8080/api/volume \
  -H 'Content-Type: application/json' -d '{"volume":65}'
```

`GET` liefert `volume` und `default_volume`. `PUT` bestätigt `applies: "next_notification"` und `persistent: false`. Dies ändert die Notification-Lautstärke, nicht dauerhaft die Lautstärken deiner Snapclients.

### Status

| Endpunkt | Antwort |
|---|---|
| `GET /healthz` | Prozess erreichbar; kein Verfügbarkeitstest von MPD/Snapserver |
| `GET /api/sounds` | konfigurierte IDs und Dateinamen |
| `DELETE /api/history` | Historie und letzten Fehler leeren; aktive Wiedergabe/Warteschlange bleiben erhalten |
| `GET /api/status` | Zustand, aktueller Job, Warteschlangenlänge, Laufzeit-Lautstärke, MQTT-Zustand, letzte Ergebnisse/Fehler |
| `GET /api/config` | öffentliche Limits, Lautstärke und Version; keine Kennwörter |

Ein erfolgreicher Abspielaufruf liefert beispielsweise:

```json
{"status":"queued","job":{"id":"…","label":"doorbell","duration_seconds":1.2}}
```

Fehlerformat: `{"error":"…"}`. Statuscodes: `400` ungültige Anfrage, `403` fremder Browser-Origin, `404` unbekannte ID, `413` zu große Datei, `422` nicht unterstützte/defekte/zu lange Audiodatei, `429` volle Warteschlange, `502` Downloadfehler, `503` Service stoppt oder zwei Dateien werden bereits vorbereitet, `500` interner/Dateizugriffsfehler. Zwei Vorbereitungen dürfen parallel laufen; sie zählen zusätzlich zur konfigurierten Warteschlange. Snapserver-/MPD-Verbindungsfehler treten nach `202` im Worker auf und erscheinen im Status.

## MQTT

In der YAML `mqtt.enabled: true` setzen. Trigger als nackte ID:

```sh
mosquitto_pub -h localhost -t notifications/play -m doorbell -q 0
```

QoS 0, Clean Session, keine gespeicherten Offline-Nachrichten; retained Nachrichten beim Abonnieren werden verworfen. Während einer Trennung verpasste Nachrichten werden nicht nachgeholt. Einzelne Trigger dürfen verloren gehen. Keine automatische Wiederholung und keine Deduplizierung. MQTT akzeptiert IDs, keine URLs oder Datei-Payloads. Fehler werden geloggt und im UI-Status protokolliert. `client_id` muss im Broker eindeutig sein.

## Limits und Formate

Alle Limits stehen in der YAML: Standard **20 wartende Jobs**, **20 MiB Quelldatei**, **120 s decodiertes Audio**, **30 s Download-Timeout**. Der Timeout umfasst Download und anschließende Vorbereitung bei URL-Aufrufen. Ein zu langer Download/Stream oder eine zu große/zu lange Datei wird verworfen. MP3, übliches PCM-WAV und Ogg **Vorbis** werden anhand des Inhalts erkannt; Ogg Opus, AAC/M4A und FLAC sind nicht Teil dieser Version. Unterstützte Eingangs-Sampleraten: 8–192 kHz.

Der temporäre Speicherbedarf richtet sich nach der decodierten Dauer: Stereo-PCM benötigt rund **192 kB/s**. Bei Standardlimits sind rund 500 MiB PCM für die volle Warteschlange plus laufenden Job möglich. Compose verwendet ein auf 768 MiB begrenztes `/tmp`-tmpfs; bei größeren Limits entsprechend erhöhen. Der Mount wird bei Container-Neuerstellung verworfen. Bei nackten Binaries kann ein SIGKILL verwaiste Dateien im System-Tmp hinterlassen.

## Binary / Entwicklung

Linux amd64 und arm64; Go ab 1.27. Die Workflows und der Docker-Build verwenden Go 1.27.3.

```sh
go mod download
go vet ./...
go test -race ./...
CGO_ENABLED=0 go build -trimpath -o dist/snapnotify .
./dist/snapnotify -config config.yaml
```

Die Web-Dateien werden eingebettet. Keine npm-Installation und kein Frontend-Build. Die Tests verwenden kurze synthetische Audiodateien aus `testdata/` und simulieren MPD/Snapcast über echte lokale TCP-Verbindungen. Sie prüfen Decoder, Resampling, Limits, HTTP, FIFO-Wiedergabe, Mute, Lautstärke und Wiederherstellung einschließlich Teilfehlern und Abbruch.

MP3 wird mit `github.com/tphakala/go-mp3/pcm` decodiert, einschließlich MPEG-1, MPEG-2 und MPEG-2.5 Layer III sowie Mono/Stereo. Beep wird weiterhin für WAV/Vorbis und Resampling verwendet; der frühere MP3-Decoder wird nicht mehr in das Programm eingebunden. Die Decoder-Versionen sind festgeschrieben; Formate werden begrenzt und Dekodierfehler abgefangen. Lizenzen und Hinweise der eingebundenen Module stehen in `THIRD_PARTY_NOTICES.txt`.

## GitHub-Releases

1. Projekt in ein GitHub-Repository namens `snapnotify` übernehmen. Die Workflows müssen im zu veröffentlichenden Commit vorhanden sein.
2. In GitHub **Releases → Draft a new release** öffnen.
3. Tag wie `v0.1.0` auswählen/erstellen und Release Notes eintragen oder von GitHub generieren lassen.
4. **Publish release** klicken.

Nur `release: published` startet die Veröffentlichung. Ein Tag-Push, Entwurf oder späteres Bearbeiten startet keinen neuen Release-Build. Prereleases wie `v0.2.0-rc.1` als Prerelease markieren.

Der Workflow prüft den Code, erzeugt `snapnotify_<version>_linux_{amd64,arm64}.tar.gz` und SHA-256-Prüfsummen, veröffentlicht ein Multiarch-Image unter `ghcr.io/<owner>/<repo>` und hängt die Archive an den bereits veröffentlichten Release. Während der Workflow läuft, ist der Release daher zunächst ohne Binaries sichtbar. Stabile Releases erhalten Versions-/Minor-/Major-Tags und `latest`; Prereleases überschreiben `latest` nicht. Ein erneuter Workflow-Lauf ersetzt die Assets desselben Releases.

Es reicht das eingebaute `GITHUB_TOKEN` mit `contents: write` und `packages: write`. Für öffentliche Image-Downloads nach dem ersten Push gegebenenfalls die GHCR-Package-Sichtbarkeit auf Public stellen. Organisationsrichtlinien müssen die verwendeten Actions und Paketveröffentlichung erlauben. CI läuft unabhängig davon auf Branch-Pushes und Pull Requests und veröffentlicht nichts.

## Erster Test an deinem Setup

Mit niedriger Lautstärke starten und prüfen: lokale MPD-Datei, Internetradio, bereits pausiertes MPD, stummgeschalteter Client, zwei wartende Meldungen, Lautstärkeänderung und SIGTERM während einer Meldung. Vor allem Anfang/Ende des Tons und das Radio-Fortsetzen müssen am echten System geprüft werden. `buffer_ms` muss deinem Snapserver-Puffer entsprechen; bei hörbar abgeschnittenen Sounds `settle_ms` erhöhen. Internetradio hat keinen garantierten zeitversetzten Wiedereinstieg; das Verhalten hängt von MPD und dem Sender ab.
