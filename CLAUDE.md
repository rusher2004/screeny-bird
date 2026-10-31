# Screeny Bird

Go server and tooling behind a macOS and tvOS bird-plate screensaver. It validates, syncs and serves an approved catalog of species plates. The screensaver apps (Swift) are separate and consume the HTTP API this repo defines.

Attribution: this project builds on [Inky Bird Frame](https://github.com/veteranbv/inky-bird-frame). Keep `THIRD_PARTY_NOTICES.md` and the README credit current. Do not copy upstream code or art into this repository without updating the notices.

## Rules

These are review-blocking. They come from the owner's Go standards and, where noted, override them.

### Configuration (overrides the standards brief)

- **No defaults and no fallbacks, anywhere.** Every setting and every dependency is supplied explicitly by the caller; a constructor never replaces a missing one with a real implementation or a build variable. A missing or empty value is an error naming the setting, returned before any work starts, and every missing setting or dependency is reported together with `errors.Join`, not just the first. Flags declare no `Value`. The Dockerfile and Compose set no defaults.
- **`cmd/main.go` constructs every dependency.** It declares the `configLoader` interface it needs, builds `config.NewTOMLLoader` for the settings file, loads the settings, builds `catalog.NewDirLoader`, the upstream source wrapped in a `Retrier`, `mirror.Syncer`, the `server.Server` and its healthcheck `server.Prober` from them, and passes the ready dependencies to `commands.New`. It also owns signals (SIGINT and SIGTERM end the context `serve` runs under) and the request logger it hands the server. While these are the only implementations, `main` assumes the settings file is at `/etc/screeny/config.toml`; when an alternative loader arrives (for example one reading the environment), `main` grows the flags it needs to choose between them. Because `main` loads config before building commands, every invocation, including `version`, needs the file.
- **`commands` receives ready dependencies and knows nothing about config.** `commands.Options` is `Writer`, `Catalogs`, `Syncer`, `Server`, `Prober` and `Version`, all required. It declares its `catalogLoader`, a plain `Load()`: what locates the data, a directory, is held by the implementing struct and never appears in the interface.
- **Loaders are concrete structs built by constructors that validate their argument:** `config.NewTOMLLoader(path)` and `catalog.NewDirLoader(dir)`. `config.TOMLLoader` decodes strictly: unknown keys, wrong types and missing or empty settings are errors. Another source, such as AWS Secrets Manager, is another type in `config` with a constructor and a `Load()` method.
- **The version is injected, not discovered.** `main` owns the `version` variable the build sets with `-ldflags "-X main.version=<VERSION>"` and passes it to `commands.Options` as a required field. A binary built without it fails at startup with `ErrMissingOption` naming `version`; there is no fallback.
- **No ambient state outside `cmd/`.** No other package calls `os.Getenv`, `os.LookupEnv`, `os.Environ`, `os.ExpandEnv`, `os.Setenv`, `os.UserHomeDir`, `os.UserConfigDir` or `os.UserCacheDir`. Nothing derives an implicit directory. Tests inject values and do not use the real environment.
- Containers receive the config file the same way in development and production, as a mounted file at `/etc/screeny/config.toml`.
- Constants are for protocol invariants (for example `schema_version` 1). Anything an operator could tune is configuration.

### Errors

- Handle every error, including `Close`, `Remove` and `Flush`. No `_ = f()` without a comment saying why.
- Wrap with `fmt.Errorf("<what was being done, with identifying context>: %w", err)`. Never a bare `return err` across a package boundary; never `%v` where `%w` works.
- Lower layers neither log nor pick policy. Errors travel to `main`, which prints once and maps the exit code: `*commands.ExitError` anywhere in the chain (`errors.AsType`) sets the code (usage errors are 2), anything else is 1.
- Loops that continue past a failure collect with `errors.Join`. A cleanup error on an error path is joined in with a named return.
- Callers that must branch get an exported sentinel or typed error.
- No fallbacks: succeed explicitly or return an error. An explicit surfaced state (for example `Unknown`) is behavior, not a fallback.

### Interfaces

- Follow dependency inversion. The package that uses a capability declares the smallest interface it needs, next to the code that uses it, with exactly the methods it calls. Implementations are concrete types that do not declare interfaces for their callers. Accept interfaces, return structs. Factories return an untyped `nil, err`, never a typed nil in an interface.
- Any package may consume capabilities from some packages and provide them to others. No package is classified or restricted by role; the question in review is whether each dependency is injected through an interface its consumer declared.
- No file exists only to declare types, and no `*_iface.go`.
- `commands` is the composition root. It adapts implementations to the interfaces it needs. Command code has no system-specific branching.
- Dependency inversion cannot be checked mechanically, so it is a review judgment. `tools/repoaudit` checks only the mechanical rules.

### Tests

- Tests run from outside the package: `foo_test.go` is `package foo_test`. Never test unexported identifiers. If something cannot be tested through the exported API, fix the API; never export only for a test. `package main` is the exception (it cannot be imported), as is `tools/repoaudit`, which is a `package main`.
- One `x_test.go` per `x.go`; none without its source file. `testdata/` is not a test file.
- Tests never run real subprocesses, make real network calls, read the real environment, or write outside `t.TempDir()`. Inject the clock, process runner and other seams. Use `t.Setenv` only in `package main` tests.
- Write the test first. Code that decides a risky or irreversible action (parsing, gating, policy, expiry) is fully covered.

### Safety rules

The rules in `.claude/review.md` under "Safety rules" must never be broken. Each one lands with a test in the milestone that implements its feature; a rule with no test yet is tracked in the table below.

| Rule | Test |
| --- | --- |
| No ambient env/home/config access outside `cmd/`; `cmd/` reads only named keys, never `os.Environ` or implicit directories | `tools/repoaudit` `TestRepository` |
| No flag defaults in `commands` | `tools/repoaudit` `TestRepository` |
| No type-only files | `tools/repoaudit` `TestRepository` |
| Usage errors exit 2, others exit 1 | `commands_test`, `cmd/main_test` |
| Review gate, location-free review, private fields and local paths, image size/format/metadata, checksums, unexpected files, symlinks, schema version, index matches manifests | `catalog` tests (`review_test`, `privacy_test`, `image_test`, `manifest_test`, `index_test`, `catalog_test`) |
| Approved taxa are immutable: sync only adds, and refuses and reports a species that changed upstream or was renamed | `mirror` tests |
| Sync is all-or-nothing for the store: a failed download, validation or rebuild leaves it untouched. A failed sync keeps its verified downloads, per commit, and the next run resumes from them; one sync at a time | `mirror`, `fsutil` tests |
| Only transient upstream failures are retried, a bounded number of times; a permanent one, or a canceled context, never is | `upstream` tests |
| Upstream is read at one commit, and every download is checked against the upstream index; fetch paths and commits are validated | `upstream`, `mirror` tests |
| Only catalog images are served, and only after their bytes match the catalog's SHA-256; a tampered, missing or symlinked file is a 503, never sent | `server`, `catalog` tests |
| GET and HEAD only (405 otherwise); `nosniff` on every response; CORS only for exactly listed origins, never a wildcard or credentials, never on `/healthz`; the catalog response has exactly the documented fields | `server` tests |
| A catalog that does not validate stops the server before it listens; a failed reload keeps serving the last validated catalog and `/healthz` reports it with a 503 | `server` tests |

### Docs

When a change affects commands, flags, exit codes or config keys, update `README.md`, `config.example.toml` and this file in the same change. Architecture changes update the package table. Release-significant changes (non-test Go, `go.mod`, `go.sum`, `.goreleaser.yaml`, `Dockerfile`) require a strictly higher `VERSION`; CI enforces it.

## Packages

| Path | Role |
| --- | --- |
| `cmd/` | `main`. Loads the settings file, constructs every dependency from it, owns signals and the request logger, runs `commands`, prints the returned error once and maps the exit code. |
| `commands/` | urfave/cli v3 command tree. `Run` returns every error. Declares the catalog loader, syncer, server and prober it needs; the version is passed in. |
| `tools/repoaudit/` | AST audit for the mechanical rules above: type-only files, ambient access outside `cmd/`, flag defaults. |
| `config/` | `Config` and its loaders. `NewTOMLLoader(path)` returns a `TOMLLoader` whose `Load()` reads the settings strictly from that TOML file: every setting required, unknown keys rejected, no defaults. |
| `catalog/` | `NewDirLoader(dir)` returns a `DirLoader` whose `RebuildIndex()` validates the tree and atomically writes the canonical `index.json`, and whose `Load()` validates the whole catalog tree in that directory, including that `index.json` matches the manifests, and returns the species, or an error joining every problem found. Sentinel errors name the rule broken. Per-file limit 32 MiB. The loading logic is unexported: `DirLoader` is the one entry point, and tests reach every case through it. |
| `server/` | `Server` serves `/healthz`, `/v1/catalog` and `/v1/assets/` from a validated catalog held in memory. Declares the store, listener, ticker and access-log interfaces it needs; reloads a changed catalog on every tick and keeps the last good one if a reload fails. Asset reads go through `os.Root`. Also `TCPListener`, `TimeTicker` and the `Prober` the healthcheck command uses. Tested end to end over in-memory pipes. |
| `upstream/` | `Source` resolves a GitHub ref to one commit and fetches raw files at it, marking transient failures (network, body read, 429, 5xx) with `ErrTransient`. `Retrier` wraps a source and retries only those, a bounded number of times, waiting through an injected sleeper. Declares the one-method HTTP client it needs; `main` builds it with no proxy, no redirects and the configured timeout. |
| `mirror/` | `Syncer` adds the species a local store lacks: reads the upstream index at one commit, stages and verifies only what is missing under `work_dir/stage-<commit>`, validates it with `catalog`, publishes by rename and rebuilds the index, or rolls back. A failed run keeps its verified staging and the next run on the same commit resumes from it; other commits' staging is removed. Never changes a species the store holds. |
| `derive/` | (planned, M3) Deterministic screensaver-sized image derivatives. |
| `fsutil/` | `WriteFileAtomic` and a kernel-released advisory file lock (`unix` build tag). |

## Targets

Linux only (amd64 and arm64), shipped as a multi-arch container image and release archives. No Windows or macOS builds. Local `go build`, `go vet` and `go test` on macOS work through the `unix` build tag, but nothing ships for it.

## Checks

Run in this order before pushing; CI mirrors it:

```bash
gofmt -l .                         # must print nothing
go fix ./... && git diff --exit-code
go generate ./... && git diff --exit-code
go vet ./...
go test ./...
go build ./...
go mod tidy && git diff --exit-code
```

Build and smoke-test the image. The version is a required build argument:

```bash
docker build --build-arg VERSION="$(cat VERSION)" -t screeny:dev .
docker run --rm -v "$PWD/config.example.toml:/etc/screeny/config.toml:ro" screeny:dev version
```

For a dev stack, copy `config.example.toml` to `config.local.toml` (gitignored) and run `VERSION="$(cat VERSION)" docker compose up --build`; `docker compose run --rm sync` runs a sync. Compose sets no defaults: the version and the mounted settings file are explicit.

The committed `.claude/settings.json` hooks run `gofmt -w` on edited Go files and `go fmt ./... && go fix ./...` when a turn ends.

## Commits and pull requests

Commits come from a person's identity. End commit messages with the attribution line the user's organization requires.
