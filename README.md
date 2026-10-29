# snapnotify

Play audio notifications through Snapcast using a web UI, HTTP or MQTT. Supports local files, URLs and uploads. MPD pause/resume is optional.

- MP3 (MPEG-1/2/2.5 Layer III), PCM WAV and Ogg Vorbis.
- One playback queue for all inputs.
- Temporary notification volume, with previous client volumes and streams restored afterwards.
- Single Go binary with an embedded UI. No FFmpeg or audio device required.

## Quick start

1. Copy `config.example.yaml` to `config.yaml` and set the Snapserver addresses.
2. Add a notification source to Snapserver, as shown below.
3. Connect snapnotify and Snapserver to the same Docker network. The Compose files use an existing network named `audio`. Create it if needed and attach Snapserver to it, or set `AUDIO_NETWORK` to your existing network name.
4. Put audio files in `sounds/` and add their IDs to the configuration. URLs and uploads work without configured files.
5. Start the published image:

```sh
cp config.example.yaml config.yaml
# Edit config.yaml before starting.
docker compose -f compose.yml pull
docker compose -f compose.yml up -d
```

Open [http://localhost:8080](http://localhost:8080). The service has no authentication and is intended for a trusted internal network. The default port binding is localhost only; change it to the host's LAN address or use an internal reverse proxy for remote access.

`compose.yml` uses `ghcr.io/desirevolution/snapnotify:latest`. To update, repeat the `pull` and `up` commands. Keep your configuration and sound files.

For a local build instead:

```sh
docker compose -f compose.dev.yml up -d --build
```

Both Compose files are standalone alternatives. Do not combine them. Configuration and sound files must be readable by container user `65532:65532`. Create `config.yaml` as a file before starting; configuration changes require a restart.

### Snapserver

Enable TCP control and add a dedicated audio input in `snapserver.conf`, keeping any existing sources:

```ini
[tcp]
enabled = true
bind_to_address = 0.0.0.0
port = 1705

[stream]
source = tcp://0.0.0.0:4953?name=notifications&codec=flac&sampleformat=48000:16:2
```

Restart Snapserver after editing. Match these settings in snapnotify:

```yaml
snapcast:
  control_address: snapserver:1705
  audio_address: snapserver:4953
  stream: notifications
  buffer_ms: 350
  settle_ms: 150
```

Port `1705` is for control and `4953` receives raw 48 kHz, 16-bit stereo PCM. FLAC is used for Snapserver-to-client transport. These ports only need to be reachable inside the Docker network. Reserve the notification source for one snapnotify instance.

Set `buffer_ms` to Snapserver's configured buffer. `settle_ms` adds time for switching and draining audio. Smaller values reduce delay but can cause clipped audio on some setups.

See `snapserver.stream.conf` for an example combining notifications and an MPD FIFO through a priority stream. Keep normal playback groups on their music/combined stream; snapnotify temporarily switches them to the notification source.

## Configuration

All options and defaults are listed in `config.example.yaml`. Unknown options are rejected.

```yaml
audio:
  directory: ./sounds
  volume: 60

sounds:
  doorbell: doorbell.mp3
  mail: mail.ogg
```

The audio directory is relative to the configuration file. Sound paths are relative to that directory. Each sound ID can be triggered through the UI, HTTP or MQTT.

Default limits are 20 queued notifications, 20 MiB per file, 120 seconds of audio and a 30-second URL preparation timeout. Files are decoded before entering the queue. Unsupported formats, including Ogg Opus, are rejected.

MPD is disabled unless an address is set:

```yaml
mpd:
  address: mpd:6600
  password: ""
```

With MPD configured, playing music is paused for notifications and resumed afterwards if the expected paused state is unchanged. An unreachable configured MPD produces a playback error. Without MPD configuration, other audio sources continue in the background.

MQTT is also optional:

```yaml
mqtt:
  enabled: true
  broker: tcp://mosquitto:1883
  topic: notifications/play
  client_id: snapnotify
```

Use `ssl://host:8883` for TLS with system CA verification. Set credentials in the configuration or override passwords through `SNAPNOTIFY_MQTT_PASSWORD` and `SNAPNOTIFY_MPD_PASSWORD`.

## Usage

The UI supports configured sounds, direct URLs and single-file uploads. Volume changes apply to the next notification and reset to the configured value on restart. **Clear** removes recent notifications and the last error without interrupting playback or changing the queue.

### HTTP

```sh
# Play a configured sound.
curl -X POST http://localhost:8080/play/doorbell

# Download and play an audio file.
curl -X POST http://localhost:8080/play-url \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.org/notification.mp3"}'

# Upload and play a file.
curl -X POST http://localhost:8080/play-file -F 'file=@notification.wav'

# Set notification volume for subsequent playback.
curl -X PUT http://localhost:8080/api/volume \
  -H 'Content-Type: application/json' -d '{"volume":65}'
```

Playback requests return `202` once the audio is prepared and queued. Later playback errors appear in the UI, status endpoint and logs. Error responses use `{"error":"message"}`.

| Endpoint | Purpose |
|---|---|
| `GET /api/sounds` | Configured sound IDs and filenames |
| `GET /api/status` | Playback, queue, MQTT status and recent results |
| `GET /api/volume` | Current and configured default volume |
| `GET /api/config` | Public settings and version |
| `DELETE /api/history` | Clear recent results and the last error |
| `GET /healthz` | Process health; does not check external services |

URLs must point to finite HTTP/HTTPS audio files, without embedded credentials. Internal URLs are allowed. Uploads and downloads are temporary and deleted after playback or failure.

### MQTT

Publish a plain sound ID to the configured topic:

```sh
mosquitto_pub -h localhost -t notifications/play -m doorbell -q 0
```

The client uses QoS 0 and a clean session. Retained messages are ignored and offline messages are not replayed. MQTT accepts IDs only.

## Playback behavior

Connected, unmuted clients in unmuted groups receive notifications. Previous streams and client volumes are restored at the end of a playback batch; detectable manual changes are preserved. Clients connecting mid-batch are not added.

The queue is kept in memory and follows the order in which preparation completes. There are no retries or priorities. Recent results and temporary volume settings also reset on restart. Graceful shutdown attempts to restore playback settings; a crash or an unavailable server can prevent restoration.

The Compose files provide a 768 MiB temporary filesystem. Increase it if raising file, duration or queue limits: decoded PCM uses about 192 kB per second. Start with a low volume and check playback on your clients, including short sounds and MPD radio streams.

## Native binary

Download the Linux archive for your architecture, extract it and create `config.yaml` from the example:

```sh
./snapnotify -config config.yaml
```

The UI is served by the binary. No separate web server is needed.

## Development and releases

Requires Go 1.27 or newer. No frontend build is needed.

```sh
go vet ./...
go test -race ./...
CGO_ENABLED=0 go build -trimpath -o dist/snapnotify .
./dist/snapnotify -config config.yaml
```

Tests cover decoding, limits, HTTP, MQTT and playback control with local TCP test peers. An optional UI smoke test is in `scripts/ui-smoke.cjs` and requires JSDOM. Dependency licenses are in `THIRD_PARTY_NOTICES.txt`.

To publish, create a version such as `v0.3.0` under **GitHub → Releases** and select **Publish release**. The workflow builds a Linux amd64/arm64 container image and binary archives with SHA-256 checksums. It pushes the image to GHCR and attaches binaries to the published release. Stable releases update `latest`; prereleases do not. Tag pushes alone do not publish.

The workflow uses `GITHUB_TOKEN`. Enable package publishing and make the GHCR package public for unauthenticated image pulls. Normal CI runs on branch pushes and pull requests without publishing.
