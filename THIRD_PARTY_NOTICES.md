# Third-party notices

Screeny Bird is licensed under the [MIT License](LICENSE). It builds on the work below.

## Inky Bird Frame

Screeny Bird exists because of [Inky Bird Frame](https://github.com/veteranbv/inky-bird-frame) by Henry Sowell. That project designed the catalog format, the research and review pipeline that produces each approved plate, and the approved plate catalog itself. Screeny Bird reimplements the catalog-handling behavior in Go for a macOS screensaver. It is a separate project and is not affiliated with or endorsed by Inky Bird Frame or its author.

- Source: <https://github.com/veteranbv/inky-bird-frame>
- Reviewed upstream revision: `8bc988b239d7ffb7d7394b1e5ad174b90f18ae1e`
- Used as: the design reference for the catalog schema, validation gates and HTTP asset-serving rules, and (once synced) the source of the approved catalog data. The `catalog sync` command records the exact upstream commit it fetched.

The upstream license, reproduced as required:

```text
MIT License

Copyright (c) 2026 Henry Sowell

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

> **Open item before any release that redistributes catalog art:** the license above covers the upstream repository. Confirm with the upstream maintainer that it extends to the generated plates and profiles, and that they are content with the way this project hosts or redistributes them. The plates are generated from licensed iNaturalist reference photos (CC0 or CC BY); each plate's `profile.json` lists its sources.

## Go dependencies

| Module | License | Source |
| --- | --- | --- |
| `github.com/urfave/cli/v3` | MIT, Copyright (c) 2023 urfave/cli maintainers | <https://github.com/urfave/cli> |
| `github.com/pelletier/go-toml/v2` | MIT, Copyright (c) 2021 - 2023 Thomas Pelletier | <https://github.com/pelletier/go-toml> |
| `github.com/getkin/kin-openapi` | MIT, Copyright (c) 2017-2018 the project authors | <https://github.com/getkin/kin-openapi> |
| `golang.org/x/image` | BSD-3-Clause, Copyright 2009 The Go Authors | <https://pkg.go.dev/golang.org/x/image> |

Installed Go modules retain the license and copyright material in their own repositories.
