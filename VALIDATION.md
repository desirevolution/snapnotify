# Validation of v0.2.0-dev

Validated in the development environment:

- `go vet ./...`
- `go test -race -count=1 -cover ./...`: passed, 76.0% statement coverage.
- MPD optional: absent/empty address disables all MPD connections; configured but unavailable MPD reports a failure before stream switching.
- History clear: removes history/errors, preserves playback/queue and accepts new events; cross-site deletion rejected.
- MPEG-2/2.5 MP3 regression tests at all six low sample rates, mono and stereo.
- MP3, WAV and Ogg Vorbis decoding; mono-to-stereo and 44.1-to-48 kHz conversion; Opus rejection; size/duration limits and temporary-file cleanup.
- Local TCP test peers for MPD and Snapcast: FIFO audio delivery, pause/resume, muted clients, manual changes, runtime volume, partial failures and cancellation.
- MQTT protocol test peer: clean session, QoS 0 subscription, ignored retained trigger and accepted live trigger.
- JSDOM UI smoke test: configured sound, URL, file upload, tabs, volume update, history clearing and escaped filenames. The test is in `scripts/ui-smoke.cjs`; it requires `jsdom` as a test-only dependency, not at runtime.
- `node --check web/app.js`.
- GitHub workflow validation with actionlint v1.7.12.
- Static, CGO-free Linux amd64 and arm64 cross-builds.
- Native amd64 binary: MPEG-2.5 playback against a local Snapcast TCP test peer without any MPD configuration, restored streams/volume, history clearing and SIGTERM shutdown.

Not exercised here:

- Actual Snapserver/MPD installations, real snapclients or audible playback.
- Browser rendering/visual layout: the browser download was unavailable; UI logic was checked using JSDOM.
- Docker image build/run and GHCR publication: this environment has no Docker runtime and no target repository was specified.
- GitHub Actions execution and native execution of the arm64 binary.

See README.md for the first test against your Docker setup. The archive includes development binaries in `dist/` and their SHA-256 checksums; the GitHub release workflow produces versioned release archives separately.
