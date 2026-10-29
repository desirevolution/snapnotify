# Changelog

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
