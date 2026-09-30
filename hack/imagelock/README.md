<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# imagelock - air-gap image lock generator

For every tagged Karta release this tool writes a digest-pinned list of the
container images an install needs, so an air-gapped cluster can mirror the exact
bits for that version.

It lives in its own Go module so its registry client (go-containerregistry) never
enters Karta's shipped dependency graph or its generated license list.

## What it does

1. Renders the Helm chart with `helm template`, every optional toggle forced on.
2. Collects every container image the render would run (operator plus hook images).
3. Classifies each against a known-name map. An unknown image stops the release,
   so a new image must be named on purpose before it can ship.
4. Resolves each tag to its digest with go-containerregistry: the multi-arch index
   digest, then the per-platform manifest digest. It resolves twice and fails if
   the two reads differ.
5. Writes one ImageLock YAML per platform. It builds the whole set in memory and
   writes only at the end, so a failure never leaves a partial lock.

## Usage

```bash
# from the repo root
make image-lock VERSION=0.2.2          # generate into ./dist
make image-lock-verify                 # offline drift check (no network, no files)
make image-lock-test                   # unit tests
```

## Output (per release tag)

```text
dist/
  imagelock-karta-vX.Y.Z-linux-amd64.yaml
  imagelock-karta-vX.Y.Z-linux-arm64.yaml
```

The YAML is the `ImageLock`: name, source tag, index digest, per-platform digest.

## Air-gap install

Download the chart and the lock for your version from the GitHub Release into the
current directory. Pick the lock for your cluster architecture - each arch has
different platform digests, so the file must match your nodes:

```bash
ARCH=amd64   # or arm64
LOCK=imagelock-karta-vX.Y.Z-linux-$ARCH.yaml   # the release asset, in the current dir
```

On a host that can reach both the public registries and the private one, copy each
image's exact digest into the private registry under its tag (skopeo shown; crane /
regctl work too):

```bash
set -euo pipefail
yq -r '.spec.images[] | .image + " " + .source' "$LOCK" | while read -r digest_ref tag_ref; do
  skopeo copy "docker://$digest_ref" "docker://$INTERNAL_REGISTRY/$tag_ref"
done
```

The chart pins images by `repository:tag` (it has no digest field), so mirroring the
digest under its tag means the tag in the private registry now points at the exact
bits. Install pointing both repositories at their full source path:

```bash
helm install karta ./karta-X.Y.Z.tgz \
  --set image.repository=$INTERNAL_REGISTRY/ghcr.io/dsx-ai-factory/workload-map/karta-operator \
  --set crdUpgrader.image.repository=$INTERNAL_REGISTRY/registry.k8s.io/kubectl
```

## Adding a new image

If a future chart change runs a new container image, `image-lock-verify` fails on
the pull request with the unknown reference. Add its repository to `knownImages`
in `main.go` with a friendly name, then the lock will include it.
