// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package replay

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/resource"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

const (
	repoRoot     = "../../.."
	recordedGlob = "../recorded_data/*/*/*/*.yaml"
)

// Each recording is a real flow the recorder captured. Walk it step by step through the recording reader:
// Karta must parse every CR, and the statuses it reads must include the state the recorder observed from
// the CR's own fields.
var _ = Describe("Karta reads the recorded state", func() {
	recordings, _ := filepath.Glob(recordedGlob)
	if len(recordings) == 0 {
		It("has recordings to replay", func() {
			Fail("no recordings under test/e2e/recorded_data; run make record-e2e")
		})
		return
	}

	for _, path := range recordings {
		path := path
		It(strings.TrimPrefix(path, "../recorded_data/"), func(ctx SpecContext) {
			r, err := recorder.OpenRecording(path)
			Expect(err).NotTo(HaveOccurred())

			name := filepath.Base(r.Recording().KartaFile)
			Expect(name).To(MatchRegexp(`^[a-zA-Z0-9._-]+\.yaml$`),
				"recording %q names a suspicious KartaFile %q", path, r.Recording().KartaFile)
			kartaYAML, err := os.ReadFile(filepath.Join(repoRoot, "docs", "catalog", name))
			Expect(err).NotTo(HaveOccurred())
			karta := &kartav1alpha1.Karta{}
			Expect(yaml.Unmarshal(kartaYAML, karta)).To(Succeed())

			for r.Next() {
				state, cr := r.State(), r.Object()
				root, err := resource.NewComponentFactoryFromObject(karta, cr).GetRootComponent()
				Expect(err).NotTo(HaveOccurred(), "Karta could not parse the %q CR", state)
				status, err := root.GetStatus(ctx)
				Expect(err).NotTo(HaveOccurred(), "Karta could not read the %q CR", state)
				Expect(status).NotTo(BeNil(), "Karta read no status from the %q CR", state)
				Expect(status.MatchedStatuses).To(ContainElement(kartav1alpha1.ResourceStatus(state)),
					"Karta read %v, recorded state was %q", status.MatchedStatuses, state)
			}
		})
	}
})
