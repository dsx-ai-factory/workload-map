// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Command imagelock renders the Karta Helm chart, resolves every container image
// the install runs to its digest, and writes one ImageLock YAML per platform.
// It lives in its own Go module so its registry client stays out of Karta's
// shipped dependency and license graph.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"sigs.k8s.io/yaml"
)

const (
	lockAPIVersion = "artifacts.run.ai/v1alpha1"
	lockKind       = "ImageLock"
	lockName       = "karta"
	lockProfile    = "standard"
)

const (
	registryAttempts = 3
	registryBackoff  = 2 * time.Second
)

// defaultStabilityReads is how many times each tag's digest is re-read and
// required to agree, guarding against a tag that moves mid-run.
const defaultStabilityReads = 10

// knownImages maps each shippable repository to the short name it gets in the
// lock. Classification is fail-closed: a repository missing here stops the
// release, so every new image must be added on purpose before it can ship.
var knownImages = map[string]string{
	"ghcr.io/dsx-ai-factory/workload-map/karta-operator": "operator",
	"registry.k8s.io/kubectl":                            "crd-upgrader",
}

// selfImages classifies the project's own images by basename so a fork can
// publish them under its own registry namespace. Third-party images stay
// exact-match only.
var selfImages = map[string]string{
	"karta-operator": "operator",
}

var sha256Digest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+`)

type platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}

func (p platform) String() string { return p.OS + "/" + p.Architecture }

type chartImage struct {
	name string // short name from knownImages
	repo string // repository, without the tag
	tag  string
}

// reference is the full image reference the chart uses, repo:tag.
func (c chartImage) reference() string { return c.repo + ":" + c.tag }

type resolvedImage struct {
	chartImage
	indexDigest       string // empty for a single-arch image
	digestPerPlatform map[platform]string
}

// digestResolver is an interface so tests can use an in-memory registry.
type digestResolver interface {
	resolve(ref string, platforms []platform) (indexDigest string, digestPerPlatform map[platform]string, err error)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("imagelock: ")
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	opts, err := parseFlags(args)
	if err != nil {
		return err
	}

	manifest, err := renderChart(opts)
	if err != nil {
		return fmt.Errorf("render chart: %w", err)
	}

	images, err := imagesFromManifest(manifest)
	if err != nil {
		return err
	}

	if opts.verifyOnly {
		log.Printf("ok: %d image(s) classified, all known", len(images))
		return nil
	}

	resolved, err := resolveImages(images, opts.platforms, remoteResolver{stabilityReads: opts.stabilityReads})
	if err != nil {
		return err
	}

	return writeLocks(opts, resolved)
}

// --- flags ---

type options struct {
	chart          string
	version        string
	platforms      []platform
	outDir         string
	helmBin        string
	setArgs        []string
	verifyOnly     bool
	stabilityReads int
}

func parseFlags(args []string) (options, error) {
	flags := flag.NewFlagSet("imagelock", flag.ContinueOnError)
	chart := flags.String("chart", "../../charts/karta", "path to the Helm chart")
	version := flags.String("version", "", "release version (with or without a leading v)")
	var platformArgs []string
	flags.Func("platform", "os/arch to lock, repeatable (default linux/amd64, linux/arm64)", func(v string) error {
		platformArgs = append(platformArgs, v)
		return nil
	})
	var setArgs []string
	flags.Func("set", "helm --set override, repeatable (applied after the built-in toggles)", func(v string) error {
		setArgs = append(setArgs, v)
		return nil
	})
	outDir := flags.String("out-dir", "../../dist", "output directory")
	helmBin := flags.String("helm", "helm", "helm binary")
	verifyOnly := flags.Bool("verify-only", false, "render and classify only; no network, no files")
	stabilityReads := flags.Int("stability-reads", defaultStabilityReads, "re-read each tag's digest this many times and require they agree")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}

	if len(platformArgs) == 0 {
		platformArgs = []string{"linux/amd64", "linux/arm64"}
	}
	platforms, err := parsePlatforms(platformArgs)
	if err != nil {
		return options{}, err
	}

	opts := options{
		chart:          *chart,
		version:        strings.TrimPrefix(*version, "v"),
		platforms:      platforms,
		outDir:         *outDir,
		helmBin:        *helmBin,
		setArgs:        setArgs,
		verifyOnly:     *verifyOnly,
		stabilityReads: *stabilityReads,
	}
	if opts.stabilityReads < 1 {
		return options{}, errors.New("--stability-reads must be at least 1")
	}
	if opts.verifyOnly {
		return opts, nil
	}
	if opts.version == "" {
		return options{}, errors.New("--version is required")
	}
	if opts.version == "latest" {
		return options{}, errors.New(`--version must be a released version, not "latest"`)
	}
	if !releaseVersion.MatchString(opts.version) {
		return options{}, fmt.Errorf("--version %q is not a release version like X.Y.Z", opts.version)
	}
	return opts, nil
}

func parsePlatforms(entries []string) ([]platform, error) {
	var platforms []platform
	for _, entry := range entries {
		os, arch, ok := strings.Cut(entry, "/")
		if !ok || os == "" || arch == "" || strings.Contains(arch, "/") {
			return nil, fmt.Errorf("bad platform %q, want os/arch", entry)
		}
		platforms = append(platforms, platform{OS: os, Architecture: arch})
	}
	if len(platforms) == 0 {
		return nil, errors.New("no platforms")
	}
	return platforms, nil
}

// --- render & classify ---

// renderChart runs `helm template` with every optional toggle forced on, so an
// image behind a default-off switch is never missed.
func renderChart(opts options) ([]byte, error) {
	args := []string{"template", "karta", opts.chart,
		"--set", "operator.enabled=true",
		"--set", "crdUpgrader.enabled=true",
		"--set", "webhook.enabled=true",
	}
	if opts.version != "" {
		args = append(args, "--set", "image.tag="+opts.version)
	}
	for _, override := range opts.setArgs {
		args = append(args, "--set", override)
	}

	helm := exec.Command(opts.helmBin, args...)
	var stderr strings.Builder
	helm.Stderr = &stderr
	rendered, err := helm.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return rendered, nil
}

func imagesFromManifest(manifest []byte) ([]chartImage, error) {
	refs, err := imageRefs(manifest)
	if err != nil {
		return nil, err
	}
	imageByName := map[string]chartImage{}
	for _, ref := range refs {
		repo := repoOf(ref)
		imageName, known := knownImages[repo]
		if !known {
			if base := repo[strings.LastIndex(repo, "/")+1:]; selfImages[base] != "" {
				imageName, known = selfImages[base], true
			}
		}
		if !known {
			return nil, fmt.Errorf("unknown image %q (repo %q): add it to knownImages before releasing", ref, repo)
		}
		tag := strings.TrimPrefix(ref, repo+":")
		if existing, seen := imageByName[imageName]; seen && existing.tag != tag {
			return nil, fmt.Errorf("image %q maps to two references: %s and %s", imageName, existing.reference(), ref)
		}
		imageByName[imageName] = chartImage{name: imageName, repo: repo, tag: tag}
	}

	images := make([]chartImage, 0, len(imageByName))
	for _, image := range imageByName {
		images = append(images, image)
	}
	sort.Slice(images, func(i, j int) bool { return images[i].name < images[j].name })
	return images, nil
}

// yamlDocument matches a "---" line, the separator helm puts between rendered
// documents. (?m) makes ^ and $ match line boundaries, so Split cuts on each one.
var yamlDocument = regexp.MustCompile(`(?m)^---\s*$`)

func imageRefs(manifest []byte) ([]string, error) {
	refs := map[string]struct{}{}
	for _, document := range yamlDocument.Split(string(manifest), -1) {
		if strings.TrimSpace(document) == "" {
			continue
		}
		var doc any
		if err := yaml.Unmarshal([]byte(document), &doc); err != nil {
			return nil, fmt.Errorf("parse rendered document: %w", err)
		}
		collectImageRefs(doc, refs)
	}

	unique := make([]string, 0, len(refs))
	for ref := range refs {
		unique = append(unique, ref)
	}
	sort.Strings(unique)
	return unique, nil
}

func collectImageRefs(node any, refs map[string]struct{}) {
	if mapping, ok := node.(map[string]any); ok {
		for key, child := range mapping {
			if key == "image" {
				if ref, ok := child.(string); ok && ref != "" {
					refs[ref] = struct{}{}
				}
			}
			collectImageRefs(child, refs)
		}
		return
	}
	if sequence, ok := node.([]any); ok {
		for _, child := range sequence {
			collectImageRefs(child, refs)
		}
	}
}

func repoOf(ref string) string {
	if at := strings.IndexByte(ref, '@'); at >= 0 {
		ref = ref[:at]
	}
	// A colon after the last slash is a tag; a colon before it is a registry port.
	slash := strings.LastIndexByte(ref, '/')
	if colon := strings.LastIndexByte(ref, ':'); colon > slash {
		return ref[:colon]
	}
	return ref
}

// --- resolve ---

// resolveImages touches no files, so a failure leaves no partial output.
func resolveImages(images []chartImage, platforms []platform, resolver digestResolver) ([]resolvedImage, error) {
	resolved := make([]resolvedImage, 0, len(images))
	for _, image := range images {
		indexDigest, digestPerPlatform, err := resolver.resolve(image.reference(), platforms)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", image.reference(), err)
		}
		if err := validateDigests(indexDigest, digestPerPlatform); err != nil {
			return nil, fmt.Errorf("resolve %s: %w", image.reference(), err)
		}
		resolved = append(resolved, resolvedImage{
			chartImage:        image,
			indexDigest:       indexDigest,
			digestPerPlatform: digestPerPlatform,
		})
	}
	return resolved, nil
}

func validateDigests(indexDigest string, digestPerPlatform map[platform]string) error {
	if indexDigest != "" && !sha256Digest.MatchString(indexDigest) {
		return fmt.Errorf("bad index digest %q", indexDigest)
	}
	for p, digest := range digestPerPlatform {
		if !sha256Digest.MatchString(digest) {
			return fmt.Errorf("bad %s digest %q", p, digest)
		}
	}
	return nil
}

type remoteResolver struct {
	stabilityReads int
}

func (r remoteResolver) resolve(ref string, platforms []platform) (string, map[platform]string, error) {
	reference, err := name.ParseReference(ref)
	if err != nil {
		return "", nil, err
	}

	descriptor, err := resolveStableDescriptor(reference, r.stabilityReads)
	if err != nil {
		return "", nil, err
	}

	if !descriptor.MediaType.IsIndex() {
		digestPerPlatform, err := singleArchDigests(descriptor, platforms)
		if err != nil {
			return "", nil, err
		}
		return "", digestPerPlatform, nil
	}

	digestPerPlatform, err := manifestDigestsPerPlatform(descriptor, platforms)
	if err != nil {
		return "", nil, err
	}
	return descriptor.Digest.String(), digestPerPlatform, nil
}

// resolveStableDescriptor reads the tag `reads` times and errors if any two reads
// return different digests. A trustworthy registry returns the same one every time;
// extra reads guard against a tag being re-pushed mid-run.
func resolveStableDescriptor(ref name.Reference, reads int) (*remote.Descriptor, error) {
	if reads < 1 {
		reads = defaultStabilityReads
	}
	first, err := fetchDescriptor(ref)
	if err != nil {
		return nil, err
	}
	for i := 1; i < reads; i++ {
		again, err := fetchDescriptor(ref)
		if err != nil {
			return nil, err
		}
		if again.Digest != first.Digest {
			return nil, fmt.Errorf("digest changed between reads: %s vs %s", first.Digest, again.Digest)
		}
	}
	return first, nil
}

func fetchDescriptor(ref name.Reference) (*remote.Descriptor, error) {
	auth := remote.WithAuthFromKeychain(authn.DefaultKeychain)
	var lastErr error
	for attempt := 1; attempt <= registryAttempts; attempt++ {
		descriptor, err := remote.Get(ref, auth)
		if err == nil {
			return descriptor, nil
		}
		lastErr = err
		if attempt < registryAttempts {
			time.Sleep(registryBackoff)
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", registryAttempts, lastErr)
}

// manifestDigestsPerPlatform picks each platform's manifest digest out of a
// multi-arch index, failing if a platform is missing or matches more than once.
func manifestDigestsPerPlatform(descriptor *remote.Descriptor, platforms []platform) (map[platform]string, error) {
	index, err := descriptor.ImageIndex()
	if err != nil {
		return nil, err
	}
	indexManifest, err := index.IndexManifest()
	if err != nil {
		return nil, err
	}

	digestPerPlatform := make(map[platform]string, len(platforms))
	for _, p := range platforms {
		var matches []string
		for _, entry := range indexManifest.Manifests {
			if entry.Platform != nil && entry.Platform.OS == p.OS && entry.Platform.Architecture == p.Architecture {
				matches = append(matches, entry.Digest.String())
			}
		}
		switch len(matches) {
		case 0:
			return nil, fmt.Errorf("no %s manifest in index", p)
		case 1:
			digestPerPlatform[p] = matches[0]
		default:
			return nil, fmt.Errorf("ambiguous %s: %d manifests match", p, len(matches))
		}
	}
	return digestPerPlatform, nil
}

// singleArchDigests maps every requested platform to a single-arch image's digest,
// failing if the image's own platform is not among those requested.
func singleArchDigests(descriptor *remote.Descriptor, platforms []platform) (map[platform]string, error) {
	image, err := descriptor.Image()
	if err != nil {
		return nil, err
	}
	config, err := image.ConfigFile()
	if err != nil {
		return nil, err
	}
	imagePlatform := platform{OS: config.OS, Architecture: config.Architecture}

	digestPerPlatform := make(map[platform]string, len(platforms))
	for _, p := range platforms {
		if p != imagePlatform {
			return nil, fmt.Errorf("image is %s only, cannot satisfy %s", imagePlatform, p)
		}
		digestPerPlatform[p] = descriptor.Digest.String()
	}
	return digestPerPlatform, nil
}

// --- write ---

type imageLock struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"metadata"`
	Spec struct {
		Profile  string        `json:"profile"`
		Platform platform      `json:"platform"`
		Images   []lockedImage `json:"images"`
	} `json:"spec"`
}

type lockedImage struct {
	Name        string `json:"name"`
	Image       string `json:"image"`
	Source      string `json:"source"`
	IndexDigest string `json:"indexDigest,omitempty"`
}

func writeLocks(opts options, resolved []resolvedImage) error {
	if err := os.MkdirAll(opts.outDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", opts.outDir, err)
	}
	for _, p := range opts.platforms {
		if err := writeLock(opts.outDir, buildLock(opts.version, p, resolved)); err != nil {
			return err
		}
	}
	log.Printf("wrote locks for %d platform(s) to %s", len(opts.platforms), opts.outDir)
	return nil
}

func buildLock(version string, p platform, resolved []resolvedImage) imageLock {
	lock := imageLock{APIVersion: lockAPIVersion, Kind: lockKind}
	lock.Metadata.Name = lockName
	lock.Metadata.Version = ensureVPrefix(version)
	lock.Spec.Profile = lockProfile
	lock.Spec.Platform = p

	for _, image := range resolved {
		lock.Spec.Images = append(lock.Spec.Images, lockedImage{
			Name:        image.name,
			Image:       image.repo + "@" + image.digestPerPlatform[p],
			Source:      image.reference(),
			IndexDigest: image.indexDigest,
		})
	}
	sort.Slice(lock.Spec.Images, func(i, j int) bool {
		return lock.Spec.Images[i].Name < lock.Spec.Images[j].Name
	})
	return lock
}

func writeLock(outDir string, lock imageLock) error {
	fileName := fmt.Sprintf("imagelock-karta-%s-%s-%s.yaml",
		lock.Metadata.Version, lock.Spec.Platform.OS, lock.Spec.Platform.Architecture)
	yamlBytes, err := yaml.Marshal(lock)
	if err != nil {
		return fmt.Errorf("marshal lock: %w", err)
	}
	path := filepath.Join(outDir, fileName)
	if err := os.WriteFile(path, yamlBytes, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func ensureVPrefix(version string) string {
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}
