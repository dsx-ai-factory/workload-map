// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package replay

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

const (
	repoRoot     = "../../.."
	recordedGlob = "../recorded_data/*/*/*/*.yaml"
)

// Each recording is a real flow the recorder captured. Walk it step by step through the recording reader:
// Karta must parse every CR and match every phase the recorder observed from the CR's own fields.
var _ = Describe("Karta reads the recorded state", func() {
	recordings, globErr := filepath.Glob(recordedGlob)
	It("has recordings to replay", func() {
		Expect(globErr).NotTo(HaveOccurred())
		Expect(recordings).NotTo(BeEmpty(), "no recordings under test/e2e/recorded_data; run make record-e2e")
	})

	for _, path := range recordings {
		path := path
		It(strings.TrimPrefix(path, "../recorded_data/"), func(ctx SpecContext) {
			r, err := recorder.OpenRecording(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(validateRecordingRun(r.Recording())).To(Succeed())

			name := filepath.Base(r.Recording().KartaFile)
			Expect(name).To(MatchRegexp(`^[a-zA-Z0-9._-]+\.yaml$`),
				"recording %q names a suspicious KartaFile %q", path, r.Recording().KartaFile)
			kartaYAML, err := os.ReadFile(filepath.Join(repoRoot, "docs", "catalog", name))
			Expect(err).NotTo(HaveOccurred())
			karta := &kartav1alpha1.Karta{}
			Expect(yaml.Unmarshal(kartaYAML, karta)).To(Succeed())

			frame := 0
			for r.Next() {
				state, cr := r.State(), r.Object()
				root, err := resource.NewComponentFactoryFromObject(karta, cr).GetRootComponent()
				Expect(err).NotTo(HaveOccurred(), "Karta could not parse the %q CR", state)
				status, err := root.GetStatus(ctx)
				Expect(err).NotTo(HaveOccurred(), "Karta could not read the %q CR", state)
				Expect(status).NotTo(BeNil(), "Karta read no status from the %q CR", state)
				Expect(validateRecordedPhases(status.MatchedStatuses, r.Phases())).To(Succeed(),
					"frame %d: recorded checkpoint was %q", frame, state)
				frame++
			}
		})
	}
})

func validateRecordingRun(recording recorder.Recording) error {
	if !recording.Result.Succeeded || len(recording.Result.Failures) > 0 {
		return fmt.Errorf("recording did not succeed: %v", recording.Result.Failures)
	}
	if !slices.ContainsFunc(recording.Events, func(event recorder.Event) bool {
		return event.Kind == recorder.EventState
	}) {
		return fmt.Errorf("recording has no STATE observations")
	}
	return nil
}

func validateRecordedPhases(actual []kartav1alpha1.ResourceStatus, phases []string) error {
	if len(phases) == 0 {
		return fmt.Errorf("recorded frame has no phases")
	}
	want := make([]kartav1alpha1.ResourceStatus, len(phases))
	for i, phase := range phases {
		want[i] = kartav1alpha1.ResourceStatus(phase)
	}
	got := slices.Clone(actual)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return fmt.Errorf("Karta read %v; recorded phases were %v", actual, phases)
	}
	return nil
}
