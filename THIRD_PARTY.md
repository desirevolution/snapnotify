# Dependencies

Runtime modules are pinned in `go.mod` and `go.sum`:

- Beep: WAV/Vorbis decoding and resampling.
- tphakala/go-mp3: MPEG-1/2/2.5 Layer III decoding.
- Eclipse Paho: MQTT client.
- yaml.v3: configuration parsing.

Linux binaries are built with `CGO_ENABLED=0`. No external decoder, sound
server or audio device library is required. Beep's speaker package is not used.

License and provenance notices are included in `THIRD_PARTY_NOTICES.txt`.

Audio fixtures in `testdata/` are synthetic tones, generated for testing.
FFmpeg was used to create them but is not required to build, test or run
snapnotify.
