// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package flows

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

var _ = describeDynamoGraphDeployment("v1alpha1", "testdata/dynamo", "hf-token-secret")
var _ = describeDynamoGraphDeployment("v1beta1", "testdata/dynamo/v1beta1", "hf-token-secret-v1beta1")

func describeDynamoGraphDeployment(version, manifestDir, secretName string) bool {
	return Describe("DynamoGraphDeployment "+version, Ordered, Label("dynamo", "dynamo-"+version), func() {
		kartaName := "nvidia-com-dynamographdeployment-" + version
		var rec *recorder.Recorder
		var fx recorder.Fixture

		BeforeAll(func(ctx SpecContext) {
			installKarta(ctx, "../../docs/catalog/"+kartaName+".yaml", kartaName)
			// The decode worker pulls env from hf-token-secret; the operator reads it from the workload's own
			// namespace, so seed it here (up.sh only creates it in default).
			ensureSecret(ctx, secretName, map[string]string{"HF_TOKEN": "dummy"})
			fx = recorder.Fixture{Operator: "dynamo", Version: operatorVersion("dynamo"), KartaName: kartaName, KartaFile: "docs/catalog/" + kartaName + ".yaml"}
			rec = recorder.New(cfg).
				SetTimeout(8*time.Minute).
				AddState(kartav1alpha1.ProgressingStatus, DynamoProgressing()).
				AddState(kartav1alpha1.PendingStatus, DynamoPending()).
				AddState(kartav1alpha1.RunningStatus, AllOf(operatorCurrentObservation, PhaseEq("successful", "status", "state")))
		})

		It("running", func(ctx SpecContext) {
			out, err := recorder.NewFlow(rec, "running", manifestDir+"/running.yaml").Through(
				recorder.Reaches(kartav1alpha1.PendingStatus),
				recorder.Reaches(kartav1alpha1.RunningStatus),
			).Run(ctx)
			Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
			Expect(err).To(Succeed())
		})

		It("updated", func(ctx SpecContext) {
			manifest := manifestDir + "/updated.yaml"
			patch, err := dynamoWorkerUpdatePatch(manifest)
			Expect(err).NotTo(HaveOccurred())
			out, err := recorder.NewFlow(rec, "updated", manifest).Through(
				recorder.Reaches(kartav1alpha1.PendingStatus).Optional(),
				recorder.Reaches(kartav1alpha1.RunningStatus).With(currentGeneration).
					Do(&recorder.Action{Type: "Update", Patch: patch}),
				// The controller keeps state pending while replacing the worker generation.
				recorder.Reaches(kartav1alpha1.PendingStatus).With(AllOf(currentGeneration, DynamoProgressing())),
				recorder.Reaches(kartav1alpha1.RunningStatus).With(dynamoRolloutComplete),
			).Run(ctx)
			Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
			Expect(err).To(Succeed())
		})

		It("pending", func(ctx SpecContext) {
			out, err := recorder.NewFlow(rec, "pending", manifestDir+"/pending.yaml").Through(
				recorder.Reaches(kartav1alpha1.PendingStatus).With(AllOf(currentGeneration, PhaseEq("pending", "status", "state")))).Run(ctx)
			Expect(rec.Save(fx, out)).Error().NotTo(HaveOccurred())
			Expect(err).To(Succeed())
		})
	})
}

func dynamoRolloutComplete(u *unstructured.Unstructured) bool {
	return currentGeneration(u) && PhaseEq("successful", "status", "state")(u) &&
		PhaseEq("Completed", "status", "rollingUpdate", "phase")(u) && !DynamoProgressing()(u)
}

// Preserve the complete component list in the beta merge patch, which replaces arrays.
func dynamoWorkerUpdatePatch(manifest string) ([]byte, error) {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := yaml.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	env := []any{map[string]any{"name": "KARTA_E2E_UPDATE", "value": "updated"}}
	switch object["apiVersion"] {
	case "nvidia.com/v1alpha1":
		if err := unstructured.SetNestedSlice(object, env, "spec", "services", "decode", "extraPodSpec", "mainContainer", "env"); err != nil {
			return nil, err
		}
	case "nvidia.com/v1beta1":
		components, _, err := unstructured.NestedSlice(object, "spec", "components")
		if err != nil {
			return nil, err
		}
		found := false
		for _, value := range components {
			component, ok := value.(map[string]any)
			if !ok || component["name"] != "decode" {
				continue
			}
			containers, _, err := unstructured.NestedSlice(component, "podTemplate", "spec", "containers")
			if err != nil {
				return nil, err
			}
			for _, value := range containers {
				container, ok := value.(map[string]any)
				if ok && container["name"] == "main" {
					container["env"] = env
					found = true
				}
			}
			if err := unstructured.SetNestedSlice(component, containers, "podTemplate", "spec", "containers"); err != nil {
				return nil, err
			}
		}
		if !found {
			return nil, fmt.Errorf("decode main container missing from %s", manifest)
		}
		if err := unstructured.SetNestedSlice(object, components, "spec", "components"); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported Dynamo apiVersion %v", object["apiVersion"])
	}
	return json.Marshal(map[string]any{"spec": object["spec"]})
}
