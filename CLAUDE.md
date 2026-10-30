# Screeny Bird

Go server and tooling behind a macOS and tvOS bird-plate screensaver. It validates, syncs and serves an approved catalog of species plates. The screensaver apps (Swift) are separate and consume the HTTP API this repo defines.

Attribution: this project builds on [Inky Bird Frame](https://github.com/veteranbv/inky-bird-frame). Keep `THIRD_PARTY_NOTICES.md` and the README credit current. Do not copy upstream code or art into this repository without updating the notices.

## Rules

These are review-blocking. They come from the owner's Go standards and, where noted, override them.

### Configuration (overrides the standards brief)

- **No defaults, anywhere.** Every setting is supplied explicitly by the caller. A missing or empty value is an error naming the setting, returned before any work starts. Flags declare no `Value`. Dockerfile and Compose set no `ENV` defaults.
- **Only `cmd/` touches ambient state.** No other package calls `os.Getenv`, `os.LookupEnv`, `os.Environ`, `os.ExpandEnv`, `os.Setenv`, `os.UserHomeDir`, `os.UserConfigDir` or `os.UserCacheDir`. `main` reads only the settings the program declares, one named key per setting, into a typed config and hands that down. It never passes the environment itself, a map of it, or a lookup function. Tests inject values; they do not use the real environment.
- Containers receive config from the runtime (Compose `env_file` in development, the orchestrator in production). Development and production use the same image path.
- Constants are for protocol invariants (for example `schema_version` 1). Anything an operator could tune is configuration.

### Errors

- Handle every error, including `Close`, `Remove` and `Flush`. No `_ = f()` without a comment saying why.
- Wrap with `fmt.Errorf("<what was being done, with identifying context>: %w", err)`. Never a bare `return err` across a package boundary; never `%v` where `%w` works.
- Lower layers neither log nor pick policy. Errors travel to `main`, which prints once and maps the exit code: `*commands.ExitError` anywhere in the chain (`errors.AsType`) sets the code (usage errors are 2), anything else is 1.
- Loops that continue past a failure collect with `errors.Join`. A cleanup error on an error path is joined in with a named return.
- Callers that must branch get an exported sentinel or typed error.
- No fallbacks: succeed explicitly or return an error. An explicit surfaced state (for example `Unknown`) is behavior, not a fallback.

### Interfaces

- The consumer declares the smallest interface it needs, in its own package, next to the code that uses it. Producer packages export concrete types and no interfaces. Accept interfaces, return structs. Factories return an untyped `nil, err`, never a typed nil in an interface.
- No file exists only to declare types, and no `*_iface.go`.
- `commands` is the composition root. It adapts producers to the interfaces it needs. Command code has no system-specific branching.
- `tools/ifaceaudit` enforces this mechanically. Its producer list lives in `tools/ifaceaudit/main.go`; update it when adding a producer package.

### Tests

- Tests run from outside the package: `foo_test.go` is `package foo_test`. Never test unexported identifiers. If something cannot be tested through the exported API, fix the API; never export only for a test. `package main` is the exception (it cannot be imported), as is `tools/ifaceaudit`, which is a `package main`.
- One `x_test.go` per `x.go`; none without its source file. `testdata/` is not a test file.
- Tests never run real subprocesses, make real network calls, read the real environment, or write outside `t.TempDir()`. Inject the clock, process runner and other seams. Use `t.Setenv` only in `package main` tests.
- Write the test first. Code that decides a risky or irreversible action (parsing, gating, policy, expiry) is fully covered.

### Safety rules

The rules in `.claude/review.md` under "Safety rules" must never be broken. Each one lands with a test in the milestone that implements its feature; a rule with no test yet is tracked in the table below.

| Rule | Test |
| --- | --- |
| No ambient env/home/config access outside `cmd/`; `cmd/` reads only named keys with `os.Getenv` or `os.LookupEnv`, never `os.Environ` or implicit directories | `tools/ifaceaudit` `TestRepository` |
| No flag defaults in `commands` | `tools/ifaceaudit` `TestRepository` |
| No type-only files or producer interfaces | `tools/ifaceaudit` `TestRepository` |
| Usage errors exit 2, others exit 1 | `commands_test`, `cmd/main_test` |
| Review gate, location-free review, private fields and local paths, image size/format/metadata, checksums, unexpected files, symlinks, schema version, index matches manifests | `catalog` tests (`review_test`, `privacy_test`, `image_test`, `manifest_test`, `index_test`, `catalog_test`) |
| Approved taxa are immutable; staged changes hold only the index and new species | Pending: M3 `upstream` sync |
| Served assets are in the active catalog and verified first; CORS off by default; GET and HEAD are side-effect free; projection exposes no private data | Pending: M2 `server` |

### Docs

When a change affects commands, flags, exit codes or config keys, update `README.md` and this file in the same change. The first setting adds a `.env.example` listing the required keys, and later settings keep it current. Architecture changes update the package table. Release-significant changes (non-test Go, `go.mod`, `go.sum`, `.goreleaser.yaml`, `Dockerfile`) require a strictly higher `VERSION`; CI enforces it.

## Packages

| Path | Role |
| --- | --- |
| `cmd/` | `main`. The one place errors are printed, exit codes chosen and the process environment read. It reads only the settings the program declares into a typed `commands.Config` and passes that down, never the whole environment. |
| `commands/` | urfave/cli v3 command tree and composition root. `Run` returns every error. Settings are fields of `Config`, each overridable by a flag; none has a default, and a missing or empty one returns `ErrMissingSetting`. |
| `tools/ifaceaudit/` | AST audit for the structural and configuration rules above. |
| `catalog/` | `Load` validates a whole catalog tree (an `fs.FS`) and returns species and the canonical index, or every problem found. Sentinel errors name the rule broken. Per-file limit 32 MiB. |
| `server/` | (planned, M2) HTTP handlers for `/healthz`, `/v1/catalog`, `/v1/assets/`. |
| `upstream/` | (planned, M3) HTTPS client for the source catalog; explicit transport and proxy. |
| `derive/` | (planned, M3) Deterministic screensaver-sized image derivatives. |
| `fsutil/` | (planned) Atomic writes and a kernel-released advisory file lock (`unix` build tag). |

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
docker run --rm screeny:dev version
```

The committed `.claude/settings.json` hooks run `gofmt -w` on edited Go files and `go fmt ./... && go fix ./...` when a turn ends.

## Commits and pull requests

Commits come from a person's identity. End commit messages with the attribution line the user's organization requires.
