<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Documentation site

The documentation site is built with [Fern](https://buildwithfern.com). The site configuration lives in this directory, and the pages live in [docs/](../docs/). Page paths in `docs.yml` start with `../docs/` because the configuration file stays in `fern/`.

The site uses the Fern NVIDIA global theme, which requires a Fern account with access to the `nvidia` organization.

## Preview locally

Install the Fern CLI at the version in `fern.config.json`, sign in, and start the preview from this directory:

```bash
npm install -g fern-api@5.167.0
fern login
fern docs dev
```

## Validate

```bash
fern check
```

The `Fern Docs CI` workflow runs `fern check` on pull requests that change `docs/` or `fern/`.
