# Changelog

## v0.3.2-dev

- Inherit Docker's target architecture when cross-building images.
- Smoke-test the built image on both amd64 and arm64 before publishing.

## v0.3.1-dev

- Choose import persistence per request with `save`, defaulting to false.
- Add unchecked save checkboxes to the URL and upload forms.
- Remove global import save options.

## v0.3.0-dev

- Discover local sound files automatically and derive IDs from filenames.
- Add `GET /api/ids`; include discovered files in the UI, HTTP and MQTT lookups.
- Optionally save validated uploads and URL downloads as reusable local sounds.
- Keep manual mappings unchanged; handle ID conflicts and duplicate imports without overwriting files.
- Refresh the UI sound library automatically and show saved IDs after importing.

## v0.2.1-dev

- Use English throughout the UI and documentation.
- Simplify setup instructions and remove environment-specific notes.
- Use the published GHCR image in `compose.yml`; move local builds to `compose.dev.yml`.

## v0.2.0-dev

- Remove the PCM cache and preload options.
- Remove promotional UI text.
- Add history clearing through the UI and HTTP API.
- Make MPD optional when no address is configured.
- Rename the example notification stream to `notifications`.

## v0.1.1-dev

- Add a local PCM cache, later removed in v0.2.0-dev.
- Support MPEG-2.5 MP3 through a new pure-Go decoder.
- Reject missing configuration bind mounts instead of creating directories.
