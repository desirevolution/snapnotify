# Dependencies

snapnotify imports only Beep's decoder/processing packages, not its speaker
package. Oto and ALSA are therefore not runtime dependencies. The compiled
Linux binaries use `CGO_ENABLED=0` and need no external decoder executable.

The code uses pinned module versions in `go.mod`/`go.sum`:

- Beep: WAV, Ogg Vorbis decoding and sample rate conversion.
- tphakala/go-mp3: pure-Go MPEG-1/2/2.5 Layer III decoder.
- Eclipse Paho: MQTT 3.1.1 client.
- yaml.v3: strict YAML configuration parsing.

Transitive decoder/networking dependencies and their license/notice texts
are included in `THIRD_PARTY_NOTICES.txt`. Paho is distributed under its
upstream EPL/EDL terms. Refer to those texts for the applicable terms.

The Beep MP3 package is not imported; its archived decoder is not linked.
The replacement MP3 decoder is MIT licensed, with minimp3 provenance and
CC0 notices included in the upstream module. Audio formats remain limited.

`testdata/tone.mp3`, `tone.ogg`, and `unsupported-opus.ogg` are synthetic
880 Hz mono tones generated for these tests. Additional `mpeg-*.mp3`
fixtures cover MPEG-2/2.5 at 8, 11.025, 12, 16, 22.05 and 24 kHz in mono
and stereo. They contain no third-party
recording. FFmpeg was used only to create these fixed test fixtures; it is
not required to build, test, or run snapnotify.
