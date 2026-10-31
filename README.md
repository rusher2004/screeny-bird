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
| `screeny catalog sync` | Add the species the upstream catalog has and the local one lacks. | Done |
| `screeny catalog derive` | Build screensaver-sized images. | Planned |
| `screeny serve` | Serve the catalog and images. | Planned |

Choosing which plate appears next belongs to the screensaver apps, not this service.

## Configuration

Settings are read from a TOML file at `/etc/screeny/config.toml`; [config.example.toml](config.example.toml) shows every one. All are required.

| Setting | Used by | Meaning |
| --- | --- | --- |
| `catalog_dir` | `catalog validate`, `catalog sync` | Directory holding the catalog's `index.json` and `species/`. |
| `work_dir` | `catalog sync` | Scratch space for sync, on the same filesystem as `catalog_dir`. |
| `upstream_repo` | `catalog sync` | GitHub repository (`owner/name`) the catalog syncs from. |
| `upstream_ref` | `catalog sync` | Branch or tag of that repository to follow. |
| `upstream_timeout_seconds` | `catalog sync` | Seconds allowed for each request to the upstream repository. |

## Packages

| Package | Role |
| --- | --- |
| `cmd` | The entry point. |
| `commands` | The command tree and the wiring that connects the other packages. |
| `config` | Loading the settings file. |
| `catalog` | Reading and validating species, manifests and the index. |
| `upstream` | Reading the source catalog from GitHub at one commit. |
| `mirror` | Adding the species the local catalog lacks. |
| `derive` | Building screensaver-sized images. |
| `server` | The HTTP interface the apps use. |
| `fsutil` | Safe file writes and locking. |

Only `derive` and `server` do not exist yet; they arrive with the commands above. `CLAUDE.md` describes the conventions the code follows.

## Credit

Screeny Bird builds on [Inky Bird Frame] by Henry Sowell, which created the plate catalog format, the research and review pipeline, and the approved catalog of species plates. This project is a Go reimplementation of the catalog handling for a screensaver rather than an e-ink frame. It is independent of, and not endorsed by, the original. See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for the license and the upstream revision this work was based on.

## License

[MIT](LICENSE).

[Inky Bird Frame]: https://github.com/veteranbv/inky-bird-frame
