# Screeny Bird

Inspired by [Inky Bird Frame]. The server and tooling behind a macOS and tvOS screensaver that shows field-journal plates of birds.

> **Status: early scaffold.** Catalog validation works. Syncing and serving are next.

## What it does

A catalog of approved bird plates, one illustrated image and profile per species, is the source of everything the screensaver shows. `screeny` is the Go service that takes that catalog from its source to the screensaver apps:

- **Validates** the catalog: manifests, checksums, image dimensions and the quality-review record behind each plate.
- **Syncs** it from the upstream catalog, verifying every file and recording exactly which upstream revision it came from.
- **Derives** screensaver-sized images for desktop and TV displays.
- **Serves** the verified catalog and images to the apps over HTTP.

| Command | Purpose | Status |
| --- | --- | --- |
| `screeny version` | Print the version. | Done |
| `screeny catalog validate` | Check the catalog against its rules. | Done |
| `screeny catalog sync` | Fetch and verify the upstream catalog. | Planned |
| `screeny catalog derive` | Build screensaver-sized images. | Planned |
| `screeny serve` | Serve the catalog and images. | Planned |

Choosing which plate appears next belongs to the screensaver apps, not this service.

## Configuration

Every setting is required.

| Setting | Used by | Meaning |
| --- | --- | --- |
| `SCREENY_CATALOG` | `catalog validate` | Directory holding the catalog's `index.json` and `species/`. |

## Packages

| Package | Role |
| --- | --- |
| `cmd` | The entry point. |
| `commands` | The command tree and the wiring that connects the other packages. |
| `catalog` | Reading and validating species, manifests and the index. |
| `upstream` | Fetching the source catalog. |
| `derive` | Building screensaver-sized images. |
| `server` | The HTTP interface the apps use. |
| `fsutil` | Safe file writes and locking. |

Only `cmd`, `commands`, `catalog` and the `tools/ifaceaudit` repository checks exist so far; the rest arrive with the commands above. `CLAUDE.md` describes the conventions the code follows.

## Credit

Screeny Bird builds on [Inky Bird Frame] by Henry Sowell, which created the plate catalog format, the research and review pipeline, and the approved catalog of species plates. This project is a Go reimplementation of the catalog handling for a screensaver rather than an e-ink frame. It is independent of, and not endorsed by, the original. See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for the license and the upstream revision this work was based on.

## License

[MIT](LICENSE).

[Inky Bird Frame]: https://github.com/veteranbv/inky-bird-frame
