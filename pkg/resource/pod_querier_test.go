// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package resource

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/jq/execution"
)

var _ = Describe("PodQuerier", func() {
	var (
		ctx     context.Context
		testPod corev1.Pod
		querier *PodQuerier
	)

	BeforeEach(func() {
		ctx = context.Background()

		// Create a test pod with labels and annotations
		testPod = corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				Labels: map[string]string{
					"component":   "worker",
					"app":         "pytorch",
					"version":     "v1.0",
					"environment": "production",
				},
				Annotations: map[string]string{
					"config": "high-memory",
					"owner":  "team-ai",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "main",
						Image: "pytorch:latest",
					},
				},
			},
		}

		querier = NewPodQuerier(&testPod)
	})

	Describe("ExtractGroupKeys", func() {
		Context("with valid key paths", func() {
			It("should extract single group key", func() {
				keyPaths := []string{".metadata.labels.app"}

				keys, err := querier.ExtractGroupKeys(ctx, keyPaths)

				Expect(err).ToNot(HaveOccurred())
				Expect(keys).To(HaveLen(1))
				Expect(keys[0]).To(Equal("pytorch"))
			})

			It("should extract multiple group keys", func() {
				keyPaths := []string{
					".metadata.labels.app",
					".metadata.labels.version",
					".metadata.namespace",
				}

				keys, err := querier.ExtractGroupKeys(ctx, keyPaths)

				Expect(err).ToNot(HaveOccurred())
				Expect(keys).To(HaveLen(3))
				Expect(keys[0]).To(Equal("pytorch"))
				Expect(keys[1]).To(Equal("v1.0"))
				Expect(keys[2]).To(Equal("default"))
			})
		})

		Context("with empty key paths", func() {
			It("should return empty slice", func() {
				keys, err := querier.ExtractGroupKeys(ctx, []string{})

				Expect(err).ToNot(HaveOccurred())
				Expect(keys).To(BeEmpty())
			})
		})

		Context("with invalid key paths", func() {
			It("should return error for non-existent path", func() {
				keyPaths := []string{".metadata.labels.nonexistent"}

				keys, err := querier.ExtractGroupKeys(ctx, keyPaths)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("query result is empty"))
				Expect(keys).To(BeNil())
			})

			It("should return error for path returning multiple values", func() {
				keyPaths := []string{".metadata.labels | to_entries | .[].key"}

				keys, err := querier.ExtractGroupKeys(ctx, keyPaths)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("expected single query result"))
				Expect(keys).To(BeNil())
			})
		})
	})

	Describe("PassesFilters", func() {
		Context("with no filters", func() {
			It("should return true", func() {
				passed, err := querier.PassesFilters(ctx, []string{})

				Expect(err).ToNot(HaveOccurred())
				Expect(passed).To(BeTrue())
			})
		})

		Context("with valid filters", func() {
			It("should pass single true filter", func() {
				filters := []string{`.metadata.labels.app == "pytorch"`}

				passed, err := querier.PassesFilters(ctx, filters)

				Expect(err).ToNot(HaveOccurred())
				Expect(passed).To(BeTrue())
			})

			It("should pass multiple true filters (AND logic)", func() {
				filters := []string{
					`.metadata.labels.app == "pytorch"`,
					`.metadata.labels.version == "v1.0"`,
					`.metadata.namespace == "default"`,
				}

				passed, err := querier.PassesFilters(ctx, filters)

				Expect(err).ToNot(HaveOccurred())
				Expect(passed).To(BeTrue())
			})

			It("should handle boolean expression filters", func() {
				filters := []string{
					`.metadata.labels | has("app")`,
					`.spec.containers | length > 0`,
				}

				passed, err := querier.PassesFilters(ctx, filters)

				Expect(err).ToNot(HaveOccurred())
				Expect(passed).To(BeTrue())
			})
		})

		Context("with failing filters", func() {
			It("should fail single false filter", func() {
				filters := []string{`.metadata.labels.app == "wrong-app"`}

				passed, err := querier.PassesFilters(ctx, filters)

				Expect(err).ToNot(HaveOccurred())
				Expect(passed).To(BeFalse())
			})

			It("should fail if any filter fails (AND logic)", func() {
				filters := []string{
					`.metadata.labels.app == "pytorch"`,   // true
					`.metadata.labels.version == "wrong"`, // false
					`.metadata.namespace == "default"`,    // true
				}

				passed, err := querier.PassesFilters(ctx, filters)

				Expect(err).ToNot(HaveOccurred())
				Expect(passed).To(BeFalse())
			})

			It("should fail for non-boolean filter result", func() {
				filters := []string{`.metadata.labels.app`} // returns string, not boolean

				passed, err := querier.PassesFilters(ctx, filters)

				Expect(err).ToNot(HaveOccurred())
				Expect(passed).To(BeFalse())
			})
		})

		Context("with invalid filters", func() {
			It("should return error for invalid JQ expression", func() {
				filters := []string{`.invalid.[syntax`}

				passed, err := querier.PassesFilters(ctx, filters)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to evaluate filter"))
				Expect(passed).To(BeFalse())
			})

			It("should return error for filter returning multiple values", func() {
				filters := []string{`.metadata.labels | to_entries | .[].value == "pytorch"`}

				passed, err := querier.PassesFilters(ctx, filters)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("expected single query result"))
				Expect(passed).To(BeFalse())
			})

			It("should return false for filter with nonexistent field comparison", func() {
				filters := []string{`.metadata.labels.nonexistent == "something"`}

				passed, err := querier.PassesFilters(ctx, filters)

				Expect(err).ToNot(HaveOccurred())
				Expect(passed).To(BeFalse())
			})
		})
	})

	Describe("MatchesComponentType", func() {
		Context("when selector is nil", func() {
			It("should return false", func() {
				matches, err := querier.MatchesComponentType(ctx, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(matches).To(BeFalse())
			})
		})

		Context("when checking key existence (Value is nil)", func() {
			It("should return true for existing label keys", func() {
				selector := &v1alpha1.ComponentTypeSelector{
					KeyPath: ".metadata.labels.component",
					Value:   nil,
				}

				matches, err := querier.MatchesComponentType(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(matches).To(BeTrue())
			})

			It("should return false for non-existing label keys", func() {
				selector := &v1alpha1.ComponentTypeSelector{
					KeyPath: ".metadata.labels.nonexistent",
					Value:   nil,
				}

				matches, err := querier.MatchesComponentType(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(matches).To(BeFalse())
			})

			It("should return true for existing annotation keys", func() {
				selector := &v1alpha1.ComponentTypeSelector{
					KeyPath: ".metadata.annotations.config",
					Value:   nil,
				}

				matches, err := querier.MatchesComponentType(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(matches).To(BeTrue())
			})

			It("should return true for existing nested paths", func() {
				selector := &v1alpha1.ComponentTypeSelector{
					KeyPath: ".spec.containers[0].name",
					Value:   nil,
				}

				matches, err := querier.MatchesComponentType(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(matches).To(BeTrue())
			})
		})

		Context("when checking key-value pairs (Value is specified)", func() {
			It("should return true for matching label values", func() {
				value := "worker"
				selector := &v1alpha1.ComponentTypeSelector{
					KeyPath: ".metadata.labels.component",
					Value:   &value,
				}

				matches, err := querier.MatchesComponentType(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(matches).To(BeTrue())
			})

			It("should return false for non-matching label values", func() {
				value := "master"
				selector := &v1alpha1.ComponentTypeSelector{
					KeyPath: ".metadata.labels.component",
					Value:   &value,
				}

				matches, err := querier.MatchesComponentType(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(matches).To(BeFalse())
			})

			It("should return true for matching annotation values", func() {
				value := "high-memory"
				selector := &v1alpha1.ComponentTypeSelector{
					KeyPath: ".metadata.annotations.config",
					Value:   &value,
				}

				matches, err := querier.MatchesComponentType(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(matches).To(BeTrue())
			})

			It("should return false for non-matching annotation values", func() {
				value := "low-memory"
				selector := &v1alpha1.ComponentTypeSelector{
					KeyPath: ".metadata.annotations.config",
					Value:   &value,
				}

				matches, err := querier.MatchesComponentType(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(matches).To(BeFalse())
			})

			It("should handle special characters in values", func() {
				// Update the test pod to have a label with special characters
				testPod.Labels["special"] = "value-with-special_chars.and:colons"
				querier = NewPodQuerier(&testPod)

				value := "value-with-special_chars.and:colons"
				selector := &v1alpha1.ComponentTypeSelector{
					KeyPath: ".metadata.labels.special",
					Value:   &value,
				}

				matches, err := querier.MatchesComponentType(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(matches).To(BeTrue())
			})

			It("should handle values with quotes", func() {
				// Update the test pod to have a label with quotes
				testPod.Labels["quotes"] = `value-with-"quotes"`
				querier = NewPodQuerier(&testPod)

				value := `value-with-"quotes"`
				selector := &v1alpha1.ComponentTypeSelector{
					KeyPath: ".metadata.labels.quotes",
					Value:   &value,
				}

				matches, err := querier.MatchesComponentType(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(matches).To(BeTrue())
			})

			It("should return false for non-existing keys with values", func() {
				value := "any-value"
				selector := &v1alpha1.ComponentTypeSelector{
					KeyPath: ".metadata.labels.nonexistent",
					Value:   &value,
				}

				matches, err := querier.MatchesComponentType(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(matches).To(BeFalse())
			})
		})

		Context("when using complex JQ paths", func() {
			It("should work with array indexing", func() {
				value := "main"
				selector := &v1alpha1.ComponentTypeSelector{
					KeyPath: ".spec.containers[0].name",
					Value:   &value,
				}

				matches, err := querier.MatchesComponentType(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(matches).To(BeTrue())
			})

			It("should work with object navigation", func() {
				value := "default"
				selector := &v1alpha1.ComponentTypeSelector{
					KeyPath: ".metadata.namespace",
					Value:   &value,
				}

				matches, err := querier.MatchesComponentType(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(matches).To(BeTrue())
			})
		})

		Context("when JQ expression is invalid", func() {
			It("should return an error for invalid paths", func() {
				value := "any"
				selector := &v1alpha1.ComponentTypeSelector{
					KeyPath: ".invalid[[[syntax",
					Value:   &value,
				}

				matches, err := querier.MatchesComponentType(ctx, selector)
				Expect(err).To(HaveOccurred())
				Expect(err).To(BeAssignableToTypeOf(&execution.JQParseError{}))
				Expect(matches).To(BeFalse())
			})
		})
	})

	Describe("ExtractReplicaKey", func() {
		Context("when selector is nil", func() {
			It("should return empty string and found=false", func() {
				key, found, err := querier.ExtractReplicaKey(ctx, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeFalse())
				Expect(key).To(Equal(""))
			})
		})

		Context("with valid selector", func() {
			It("should extract replica key from label", func() {
				testPod.Labels["group-index"] = "2"
				querier = NewPodQuerier(&testPod)

				selector := &v1alpha1.ReplicaSelector{
					KeyPath: `.metadata.labels["group-index"]`,
				}

				key, found, err := querier.ExtractReplicaKey(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(key).To(Equal("2"))
			})

			It("should extract replica key from annotation", func() {
				testPod.Annotations["replica-id"] = "group-0"
				querier = NewPodQuerier(&testPod)

				selector := &v1alpha1.ReplicaSelector{
					KeyPath: `.metadata.annotations["replica-id"]`,
				}

				key, found, err := querier.ExtractReplicaKey(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(key).To(Equal("group-0"))
			})
		})

		Context("with invalid selector", func() {
			It("should return error for non-existent path", func() {
				selector := &v1alpha1.ReplicaSelector{
					KeyPath: ".metadata.labels.nonexistent",
				}

				key, found, err := querier.ExtractReplicaKey(ctx, selector)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("query result is empty"))
				Expect(found).To(BeFalse())
				Expect(key).To(Equal(""))
			})

			It("should return error for invalid JQ expression", func() {
				selector := &v1alpha1.ReplicaSelector{
					KeyPath: ".invalid[[[syntax",
				}

				key, found, err := querier.ExtractReplicaKey(ctx, selector)
				Expect(err).To(HaveOccurred())
				Expect(found).To(BeFalse())
				Expect(key).To(Equal(""))
			})

			It("should return error for path returning multiple values", func() {
				selector := &v1alpha1.ReplicaSelector{
					KeyPath: ".metadata.labels | to_entries | .[].value",
				}

				key, found, err := querier.ExtractReplicaKey(ctx, selector)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("expected single query result"))
				Expect(found).To(BeFalse())
				Expect(key).To(Equal(""))
			})
		})
	})

	Describe("ExtractInstanceId", func() {
		Context("when selector is nil", func() {
			It("should return empty string and found=false", func() {
				id, found, err := querier.ExtractInstanceId(ctx, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeFalse())
				Expect(id).To(Equal(""))
			})
		})

		Context("when IdPath is empty", func() {
			It("should return empty string and found=false", func() {
				selector := &v1alpha1.ComponentInstanceSelector{
					IdPath: "",
				}

				id, found, err := querier.ExtractInstanceId(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeFalse())
				Expect(id).To(Equal(""))
			})
		})

		Context("with valid selector", func() {
			It("should extract instance id from label", func() {
				testPod.Labels["job-name"] = "indexer"
				querier = NewPodQuerier(&testPod)

				selector := &v1alpha1.ComponentInstanceSelector{
					IdPath: `.metadata.labels["job-name"]`,
				}

				id, found, err := querier.ExtractInstanceId(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(id).To(Equal("indexer"))
			})

			It("should extract instance id from annotation", func() {
				selector := &v1alpha1.ComponentInstanceSelector{
					IdPath: ".metadata.annotations.config",
				}

				id, found, err := querier.ExtractInstanceId(ctx, selector)
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(id).To(Equal("high-memory"))
			})
		})

		Context("with invalid selector", func() {
			It("should return error for non-existent path", func() {
				selector := &v1alpha1.ComponentInstanceSelector{
					IdPath: ".metadata.labels.nonexistent",
				}

				id, found, err := querier.ExtractInstanceId(ctx, selector)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("query result is empty"))
				Expect(found).To(BeFalse())
				Expect(id).To(Equal(""))
			})

			It("should return error for invalid JQ expression", func() {
				selector := &v1alpha1.ComponentInstanceSelector{
					IdPath: ".invalid[[[syntax",
				}

				id, found, err := querier.ExtractInstanceId(ctx, selector)
				Expect(err).To(HaveOccurred())
				Expect(found).To(BeFalse())
				Expect(id).To(Equal(""))
			})

			It("should return error for path returning multiple values", func() {
				selector := &v1alpha1.ComponentInstanceSelector{
					IdPath: ".metadata.labels | to_entries | .[].value",
				}

				id, found, err := querier.ExtractInstanceId(ctx, selector)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("expected single query result"))
				Expect(found).To(BeFalse())
				Expect(id).To(Equal(""))
			})
		})
	})

	Describe("GetMatchingInstanceId", func() {
		var pod *corev1.Pod

		BeforeEach(func() {
			pod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Labels: map[string]string{
						"job-name":     "indexer",
						"service-name": "api",
						"component":    "worker",
					},
					Annotations: map[string]string{
						"nvidia.com/dynamo-component": "worker-group-1",
					},
				},
			}
		})

		Context("with valid instance selector", func() {
			It("should extract instance ID from pod label", func() {
				querier := NewPodQuerier(pod)
				instanceSelector := &v1alpha1.ComponentInstanceSelector{
					IdPath: ".metadata.labels[\"job-name\"]",
				}
				instanceIds := []string{"indexer", "processor"}

				result, err := querier.GetMatchingInstanceId(ctx, instanceSelector, instanceIds)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal("indexer"))
			})

			It("should extract instance ID from pod annotation", func() {
				querier := NewPodQuerier(pod)
				instanceSelector := &v1alpha1.ComponentInstanceSelector{
					IdPath: ".metadata.annotations.\"nvidia.com/dynamo-component\"",
				}
				instanceIds := []string{"worker-group-1", "worker-group-2"}

				result, err := querier.GetMatchingInstanceId(ctx, instanceSelector, instanceIds)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal("worker-group-1"))
			})

			It("should extract instance ID from nested pod spec field", func() {
				pod.Spec = corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "worker",
							Env: []corev1.EnvVar{
								{Name: "GROUP_NAME", Value: "cache"},
							},
						},
					},
				}

				querier := NewPodQuerier(pod)
				instanceSelector := &v1alpha1.ComponentInstanceSelector{
					IdPath: ".spec.containers[0].env[] | select(.name == \"GROUP_NAME\") | .value",
				}
				instanceIds := []string{"api", "worker", "cache"}

				result, err := querier.GetMatchingInstanceId(ctx, instanceSelector, instanceIds)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal("cache"))
			})
		})

		Context("with invalid instance selector", func() {
			It("should return error when JQ path is invalid", func() {
				querier := NewPodQuerier(pod)
				instanceSelector := &v1alpha1.ComponentInstanceSelector{
					IdPath: ".invalid..path",
				}
				instanceIds := []string{"indexer", "processor"}

				result, err := querier.GetMatchingInstanceId(ctx, instanceSelector, instanceIds)

				Expect(err).To(HaveOccurred())
				Expect(result).To(Equal(""))
				Expect(err.Error()).To(ContainSubstring("failed to parse JQ expression"))
			})

			It("should return error when JQ returns multiple results", func() {
				pod.Labels["duplicate-key"] = "value1"
				pod.Annotations["duplicate-key"] = "value2"

				querier := NewPodQuerier(pod)
				instanceSelector := &v1alpha1.ComponentInstanceSelector{
					IdPath: ".metadata | (.labels, .annotations) | .\"duplicate-key\"",
				}
				instanceIds := []string{"value1", "value2"}

				result, err := querier.GetMatchingInstanceId(ctx, instanceSelector, instanceIds)

				Expect(err).To(HaveOccurred())
				Expect(result).To(Equal(""))
				Expect(err.Error()).To(ContainSubstring("expected single query result"))
			})

			It("should return error when JQ returns no results", func() {
				querier := NewPodQuerier(pod)
				instanceSelector := &v1alpha1.ComponentInstanceSelector{
					IdPath: ".metadata.labels.nonexistent",
				}
				instanceIds := []string{"indexer", "processor"}

				result, err := querier.GetMatchingInstanceId(ctx, instanceSelector, instanceIds)

				Expect(err).To(HaveOccurred())
				Expect(result).To(Equal(""))
				Expect(err.Error()).To(ContainSubstring("query result is empty"))
			})
		})

		Context("instance ID validation", func() {
			It("should return InstanceNotFoundError when extracted value not in instance IDs list", func() {
				querier := NewPodQuerier(pod)
				instanceSelector := &v1alpha1.ComponentInstanceSelector{
					IdPath: ".metadata.labels[\"job-name\"]",
				}
				instanceIds := []string{"processor", "validator"} // "indexer" not in list

				result, err := querier.GetMatchingInstanceId(ctx, instanceSelector, instanceIds)

				Expect(err).To(HaveOccurred())
				Expect(result).To(Equal(""))

				var instanceNotFoundErr InstanceNotFoundError
				Expect(errors.As(err, &instanceNotFoundErr)).To(BeTrue())
				Expect(string(instanceNotFoundErr)).To(ContainSubstring("could not match instance id"))
			})

			It("should handle numeric values by converting to string", func() {
				pod.Labels["replica-id"] = "3"

				querier := NewPodQuerier(pod)
				instanceSelector := &v1alpha1.ComponentInstanceSelector{
					IdPath: ".metadata.labels[\"replica-id\"] | tonumber",
				}
				instanceIds := []string{"1", "2", "3", "4"}

				result, err := querier.GetMatchingInstanceId(ctx, instanceSelector, instanceIds)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal("3"))
			})
		})

		Context("without instance selector", func() {
			It("should match single instance with empty ID when no selector provided", func() {
				querier := NewPodQuerier(pod)
				instanceIds := []string{""} // Single instance with empty ID

				result, err := querier.GetMatchingInstanceId(ctx, nil, instanceIds)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(""))
			})

			It("should return error when no selector provided but multiple instance IDs exist", func() {
				querier := NewPodQuerier(pod)
				instanceIds := []string{"worker-1", "worker-2"} // Multiple instances

				result, err := querier.GetMatchingInstanceId(ctx, nil, instanceIds)

				Expect(err).To(HaveOccurred())
				Expect(result).To(Equal(""))
				Expect(err.Error()).To(ContainSubstring("no instance selector provided but instance ids are not empty"))
			})

			It("should return error when no selector provided with single non-empty instance ID", func() {
				querier := NewPodQuerier(pod)
				instanceIds := []string{"worker-1"} // Single non-empty instance

				result, err := querier.GetMatchingInstanceId(ctx, nil, instanceIds)

				Expect(err).To(HaveOccurred())
				Expect(result).To(Equal(""))
				Expect(err.Error()).To(ContainSubstring("no instance selector provided but instance ids are not empty"))
			})
		})
	})
})
