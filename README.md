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
| `screeny catalog derive` | Build screensaver-sized images for the species that lack one. | Done |
| `screeny serve` | Serve the validated catalog and images over HTTP. | Done |
| `screeny healthcheck` | Exit zero only if the running server is healthy. | Done |

Choosing which plate appears next belongs to the screensaver apps, not this service.

## HTTP API

| Route | Response |
| --- | --- |
| `GET /healthz` | Liveness and the served catalog's size. `503` if the latest catalog reload failed while an older valid catalog is still being served. |
| `GET /v1/catalog` | The species list: `schema_version`, `generated_at`, and per species its names, slug, approval time, the paths and SHA-256 of its portrait and display images, and its `derived` screensaver-sized image (`path`, `sha256`, `width`, `height`). |
| `GET /v1/derived/<path>` | One derived screensaver-sized PNG named by a species' `derived.path`, sent only after its bytes match the recorded checksum. `?sha256=<digest>` works the same way as for assets. |
| `GET /v1/assets/<path>` | One PNG listed in the catalog, sent only after its bytes match the catalog's checksum. Add `?sha256=<digest>` to make the response cacheable forever. |

Only `GET` and `HEAD` are accepted. Responses carry an `ETag` and answer `If-None-Match`. Browser access from another origin is off unless that exact origin is configured.

## Configuration

Settings are read from a TOML file at `/etc/screeny/config.toml`; [config.example.toml](config.example.toml) shows every one. All are required.

| Setting | Used by | Meaning |
| --- | --- | --- |
| `catalog_dir` | `catalog validate`, `catalog sync` | Directory holding the catalog's `index.json` and `species/`. |
| `work_dir` | `catalog sync` | Scratch space for sync, on the same filesystem as `catalog_dir`. |
| `derived_dir` | `catalog derive` | Where derived images are written. It must be outside `catalog_dir`. |
| `derive_width`, `derive_height` | `catalog derive` | Pixel size of every derived image. They are never replaced, so changing the size means removing `derived_dir` first. |
| `upstream_repo` | `catalog sync` | GitHub repository (`owner/name`) the catalog syncs from. |
| `upstream_ref` | `catalog sync` | Branch or tag of that repository to follow. |
| `upstream_timeout_seconds` | `catalog sync` | Seconds allowed for each request to the upstream repository. |
| `upstream_attempts` | `catalog sync` | How many times each upstream request is tried in all, counting the first. |
| `upstream_retry_delay_seconds` | `catalog sync` | Seconds to wait between those attempts. |
| `listen_address` | `serve`, `healthcheck` | Address the HTTP server listens on, as `host:port`. |
| `read_header_timeout_seconds` | `serve` | Seconds a client may take to send request headers. |
| `write_timeout_seconds` | `serve` | Seconds allowed to write one response. |
| `shutdown_timeout_seconds` | `serve` | Seconds in-flight requests get to finish on shutdown. |
| `reload_interval_seconds` | `serve` | Seconds between checks for a changed catalog. |
| `healthcheck_timeout_seconds` | `healthcheck` | Seconds allowed for the healthcheck request. |
| `cors_allowed_origins` | `serve` | Browser origins allowed to read the catalog and images. An empty list allows none. |

## Running locally

You need Docker. The stack runs the same image as production, and nothing has a default: you give it the version and a settings file.

Copy the example settings. `config.local.toml` is gitignored, and its `listen_address` must stay `:8080` to match the port the stack publishes.

```bash
cp config.example.toml config.local.toml
```

Fetch the catalog into the stack's data volume. The first sync downloads the whole upstream catalog, about 1.1 GB, so it takes several minutes. A sync that fails publishes nothing but keeps what it downloaded, so run it again and it resumes where it stopped. Progress goes to stderr as it runs: the upstream commit, how many species are missing, one line per species staged (`reused=true` for ones an earlier run already downloaded), and a warning before each retry. That staging can take up to the same space in the data volume until a sync succeeds.

```bash
VERSION="$(cat VERSION)" docker compose run --rm sync
```

Build the screensaver-sized images. Each upright portrait plate is scaled whole to fit `derive_width` by `derive_height` and padded with a solid color taken from its own border, so nothing is cropped. This takes a minute or two and logs one line per image. Derived images are never rewritten, so later runs only add the new species.

```bash
VERSION="$(cat VERSION)" docker compose run --rm derive
```

Start the server.

```bash
VERSION="$(cat VERSION)" docker compose up --build
```

Try it from another terminal.

```bash
curl localhost:8080/healthz
```

```bash
curl localhost:8080/v1/catalog
```

To pick up new upstream species later, run the sync command again, then derive. The server needs every species to have a derived image, so after a sync it keeps serving the old catalog and `/healthz` reports 503 until derive has run; it notices the finished derive within `reload_interval_seconds`. It also refuses to start until `catalog derive` has run once. To stop, press Ctrl+C or run `docker compose down`, which keeps the catalog volume. Add `-v` to delete it.

Without Docker, build the binary with a version and put the settings at `/etc/screeny/config.toml`, the one place it looks. Then run `./screeny catalog sync`, `./screeny catalog derive`, `./screeny catalog validate` and `./screeny serve`.

```bash
go build -ldflags "-X main.version=$(cat VERSION)" -o screeny ./cmd
```

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

`CLAUDE.md` describes the conventions the code follows.

## Credit

Screeny Bird builds on [Inky Bird Frame] by Henry Sowell, which created the plate catalog format, the research and review pipeline, and the approved catalog of species plates. This project is a Go reimplementation of the catalog handling for a screensaver rather than an e-ink frame. It is independent of, and not endorsed by, the original. See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for the license and the upstream revision this work was based on.

## License

[MIT](LICENSE).

[Inky Bird Frame]: https://github.com/veteranbv/inky-bird-frame
