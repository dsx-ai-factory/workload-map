// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const renderedManifest = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: karta-operator
spec:
  template:
    spec:
      initContainers:
        - name: setup
          image: ghcr.io/dsx-ai-factory/workload-map/karta-operator:1.2.3
      containers:
        - name: manager
          image: ghcr.io/dsx-ai-factory/workload-map/karta-operator:1.2.3
---
apiVersion: batch/v1
kind: Job
metadata:
  name: karta-crd-upgrader
spec:
  template:
    spec:
      containers:
        - name: apply
          image: registry.k8s.io/kubectl:v1.34.0
`

func TestImageRefs(t *testing.T) {
	got, err := imageRefs([]byte(renderedManifest))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ghcr.io/dsx-ai-factory/workload-map/karta-operator:1.2.3",
		"registry.k8s.io/kubectl:v1.34.0",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("imageRefs = %v, want %v", got, want)
	}
}

func TestImageRefsPropagatesParseError(t *testing.T) {
	if _, err := imageRefs([]byte("foo: [unterminated")); err == nil {
		t.Fatal("want parse error for malformed document, got nil")
	}
}

func TestRepoOf(t *testing.T) {
	cases := map[string]string{
		"ghcr.io/dsx-ai-factory/workload-map/karta-operator:1.2.3":                             "ghcr.io/dsx-ai-factory/workload-map/karta-operator",
		"registry.k8s.io/kubectl:v1.34.0":                                       "registry.k8s.io/kubectl",
		"localhost:5000/foo/bar:tag":                                            "localhost:5000/foo/bar",
		"ghcr.io/dsx-ai-factory/workload-map/karta-operator@sha256:" + strings.Repeat("a", 64): "ghcr.io/dsx-ai-factory/workload-map/karta-operator",
	}
	for in, want := range cases {
		if got := repoOf(in); got != want {
			t.Errorf("repoOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestImagesFromManifestRejectsUnknown(t *testing.T) {
	manifest := "kind: Pod\nspec:\n  containers:\n    - image: docker.io/library/nginx:1.25\n"
	_, err := imagesFromManifest([]byte(manifest))
	if err == nil || !strings.Contains(err.Error(), "unknown image") {
		t.Fatalf("want unknown image error, got %v", err)
	}
}

func TestImagesFromManifestRejectsConflictingRefs(t *testing.T) {
	manifest := `
kind: Pod
spec:
  containers:
    - image: ghcr.io/dsx-ai-factory/workload-map/karta-operator:1.2.3
---
kind: Pod
spec:
  containers:
    - image: ghcr.io/dsx-ai-factory/workload-map/karta-operator:9.9.9
`
	_, err := imagesFromManifest([]byte(manifest))
	if err == nil || !strings.Contains(err.Error(), "two references") {
		t.Fatalf("want conflicting-refs error, got %v", err)
	}
}

func TestImagesFromManifestClassifiesKnown(t *testing.T) {
	got, err := imagesFromManifest([]byte(renderedManifest))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].name != "crd-upgrader" || got[1].name != "operator" {
		t.Fatalf("imagesFromManifest = %+v", got)
	}
}

func TestParsePlatformsRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"", ",", "linux/", "/amd64", "linux/amd64/extra", "linux"} {
		if _, err := parsePlatforms([]string{bad}); err == nil {
			t.Errorf("parsePlatforms(%q): want error, got nil", bad)
		}
	}
	if _, err := parsePlatforms([]string{"linux/amd64", "linux/arm64"}); err != nil {
		t.Errorf("valid platforms rejected: %v", err)
	}
}

func TestParseFlagsRejectsBadVersion(t *testing.T) {
	for _, bad := range []string{"latest", "vlatest", "foo", "V0.2.2"} {
		if _, err := parseFlags([]string{"--version", bad}); err == nil {
			t.Errorf("--version %q: want error, got nil", bad)
		}
	}
	for _, good := range []string{"1.2.3", "v0.2.2", "0.2.2-rc1"} {
		if _, err := parseFlags([]string{"--version", good}); err != nil {
			t.Errorf("--version %q rejected: %v", good, err)
		}
	}
}

func TestParseFlagsRejectsBadStabilityReads(t *testing.T) {
	if _, err := parseFlags([]string{"--version", "1.2.3", "--stability-reads", "0"}); err == nil {
		t.Fatal("want error for --stability-reads 0, got nil")
	}
	if _, err := parseFlags([]string{"--version", "1.2.3", "--stability-reads", "5"}); err != nil {
		t.Fatalf("valid stability-reads rejected: %v", err)
	}
}

func TestResolveMultiArch(t *testing.T) {
	reg := httptest.NewServer(registry.New())
	defer reg.Close()
	ref := mustHost(t, reg.URL) + "/karta/karta-operator:1.2.3"

	amd64, err := random.Image(256, 1)
	if err != nil {
		t.Fatal(err)
	}
	arm64, err := random.Image(256, 1)
	if err != nil {
		t.Fatal(err)
	}
	idx := mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{Add: amd64, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}}},
		mutate.IndexAddendum{Add: arm64, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "arm64"}}},
	)
	tag, err := name.NewTag(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.WriteIndex(tag, idx); err != nil {
		t.Fatal(err)
	}

	plats := []platform{{OS: "linux", Architecture: "amd64"}, {OS: "linux", Architecture: "arm64"}}
	indexDigest, per, err := (remoteResolver{}).resolve(ref, plats)
	if err != nil {
		t.Fatal(err)
	}

	wantIndex, _ := idx.Digest()
	if indexDigest != wantIndex.String() {
		t.Errorf("index digest = %s, want %s", indexDigest, wantIndex)
	}
	amdDigest, _ := amd64.Digest()
	armDigest, _ := arm64.Digest()
	if per[plats[0]] != amdDigest.String() {
		t.Errorf("amd64 digest = %s, want %s", per[plats[0]], amdDigest)
	}
	if per[plats[1]] != armDigest.String() {
		t.Errorf("arm64 digest = %s, want %s", per[plats[1]], armDigest)
	}
}

func TestResolveSingleArch(t *testing.T) {
	reg := httptest.NewServer(registry.New())
	defer reg.Close()
	ref := mustHost(t, reg.URL) + "/foo/bar:1.0.0"
	img := pushSingleArch(t, ref, "linux", "amd64")

	amd64 := platform{OS: "linux", Architecture: "amd64"}
	indexDigest, per, err := (remoteResolver{}).resolve(ref, []platform{amd64})
	if err != nil {
		t.Fatal(err)
	}
	if indexDigest != "" {
		t.Errorf("index digest = %q, want empty for single-arch", indexDigest)
	}
	want, _ := img.Digest()
	if per[amd64] != want.String() {
		t.Errorf("digest = %s, want %s", per[amd64], want)
	}
}

func TestResolveSingleArchRejectsMismatch(t *testing.T) {
	reg := httptest.NewServer(registry.New())
	defer reg.Close()
	ref := mustHost(t, reg.URL) + "/foo/bar:1.0.0"
	pushSingleArch(t, ref, "linux", "amd64")

	_, _, err := (remoteResolver{}).resolve(ref, []platform{{OS: "linux", Architecture: "arm64"}})
	if err == nil || !strings.Contains(err.Error(), "cannot satisfy") {
		t.Fatalf("want platform-mismatch error, got %v", err)
	}
}

// pushSingleArch pushes a plain (non-index) image stamped with the given platform.
func pushSingleArch(t *testing.T, ref, os, arch string) v1.Image {
	t.Helper()
	img, err := random.Image(256, 1)
	if err != nil {
		t.Fatal(err)
	}
	config, err := img.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	config = config.DeepCopy()
	config.OS = os
	config.Architecture = arch
	img, err = mutate.ConfigFile(img, config)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := name.NewTag(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(tag, img); err != nil {
		t.Fatal(err)
	}
	return img
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}
