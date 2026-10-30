# Review rules

Apply these to every pull request, on top of `CLAUDE.md`. Report only what the change introduces. Do not report what CI already enforces.

## Important (must fix)

- A default value for any setting, or any read of the process environment, home or config directory outside `cmd/`. In `cmd/`, anything but a named `os.Getenv` or `os.LookupEnv` read, or the whole environment, a map of it or a lookup function passed down.
- A broken dependency inversion: an interface declared away from the code that consumes it, a function, constructor or struct field taking another package's concrete type where a small interface of only the methods it calls would do, or an interface with methods none of its callers use. A typed nil returned through an interface.
- An error that is ignored, returned bare across a package boundary, or logged and swallowed below `main`. `%v` or `%s` where `%w` works.
- A test in the package it tests, a test of an unexported identifier, a test that runs a real process, touches the network, or writes outside `t.TempDir()`.
- A change described as a refactor that alters flags, config keys, exit codes or error strings.
- Any weakening of a safety rule below.

## Safety rules (must never happen)

- An approved catalog species is replaced, edited or removed implicitly.
- An entry is accepted without a passing, sourced, bounded-generation, location-free quality review.
- A staged or synced catalog change contains anything but the index and new species files, or any private field or local path.
- Image dimensions, format or checksums go unchecked.
- The server sends an asset that is not in the active catalog, or before its SHA-256 is verified.
- An asset or archive path escapes its directory.
- The served catalog contains a field outside the documented projection, or any location, provider data or credential.
- Cross-origin access is enabled by default, uses a wildcard, or is credentialed.
- A GET or HEAD request changes state.
- An unsupported catalog schema version is accepted.
- Private state is written non-atomically or with permissions wider than 0600.
- Contributed images or JSON are executed or trusted.
- A workflow runs pull-request code with secrets or a write token, a third-party action is not pinned to a full commit SHA, or untrusted input is interpolated into a `run:` step.

## Nit (at most five)

- Naming and comment wording.
