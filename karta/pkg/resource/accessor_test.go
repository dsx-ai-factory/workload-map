// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package resource

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/jq/execution"
	"github.com/dsx-ai-factory/workload-map/test/types"
)

const (
	// Component names
	masterComponentName  = "master"
	workerComponentName  = "worker"
	jobComponentName     = "job"
	serviceComponentName = "service"

	// Container names
	trainerContainerName   = "trainer"
	indexerContainerName   = "indexer"
	processorContainerName = "processor"

	// Service names
	apiServiceName    = "api"
	workerServiceName = "worker"
	cacheServiceName  = "cache"

	// Label keys
	roleLabel        = "role"
	serviceNameLabel = "service-name"
	appLabel         = "app"

	// Label values
	masterRole    = "master"
	workerRole    = "worker"
	indexerRole   = "indexer"
	processorRole = "processor"

	// App values
	jobgroupApp = "jobgroup"
	reactorApp  = "reactor"

	// Error message substrings
	noPodTemplateSpecError = "does not have pod template spec definition"
	noPodSpecError         = "does not have pod spec definition"
	noMetadataError        = "does not have pod metadata definition"
	noFragmentedSpecError  = "does not have fragmented pod spec definition"
	noSpecDefinitionError  = "does not have spec definition"
	noScaleError           = "does not have scale definition"
)

func accessorForObject(
	karta *v1alpha1.Karta,
	object KubernetesObject,
	componentName string,
) (*Accessor, *Component) {
	accessor := NewAccessor(execution.NewDefaultRunner(object))
	factory := NewComponentFactoryFromObject(karta, object)
	comp, err := factory.GetComponent(componentName)
	Expect(err).NotTo(HaveOccurred())
	return accessor, comp
}

var _ = Describe("Accessor", func() {
	var (
		ctx context.Context

		pyflowKarta   *v1alpha1.Karta
		jobgroupKarta *v1alpha1.Karta
		reactorKarta  *v1alpha1.Karta

		pyflowFactory   *ComponentFactory
		jobgroupFactory *ComponentFactory
		reactorFactory  *ComponentFactory

		pyflowAccessor   *Accessor
		jobgroupAccessor *Accessor
		reactorAccessor  *Accessor
	)

	BeforeEach(func() {
		ctx = context.Background()

		pyflowKarta = types.PyFlowKarta()
		jobgroupKarta = types.JobGroupKarta()
		reactorKarta = types.ReactorKarta()

		// Create test objects
		pyflowObject := types.NewPyFlowObject()
		jobgroupObject := types.NewJobGroupObject()
		reactorObject := types.NewReactorObject()

		// Initialize factories
		pyflowFactory = NewComponentFactoryFromObject(pyflowKarta, pyflowObject)
		jobgroupFactory = NewComponentFactoryFromObject(jobgroupKarta, jobgroupObject)
		reactorFactory = NewComponentFactoryFromObject(reactorKarta, reactorObject)

		// Initialize evaluators
		pyflowAccessor = NewAccessor(execution.NewDefaultRunner(pyflowObject))
		jobgroupAccessor = NewAccessor(execution.NewDefaultRunner(jobgroupObject))
		reactorAccessor = NewAccessor(execution.NewDefaultRunner(reactorObject))
	})

	Describe("ExtractPodTemplateSpec", func() {
		Context("supported workloads", func() {
			Context("PyFlow", func() {
				It("should extract master pod template spec", func() {
					masterComp, err := pyflowFactory.GetComponent(masterComponentName)
					Expect(err).NotTo(HaveOccurred())

					result, err := pyflowAccessor.ExtractPodTemplateSpec(ctx, masterComp.definition)

					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(HaveLen(1))
					Expect(result[0].ObjectMeta.Labels[roleLabel]).To(Equal(masterRole))
					Expect(result[0].Spec.Containers).To(HaveLen(1))
					Expect(result[0].Spec.Containers[0].Name).To(Equal(trainerContainerName))
				})

				It("should extract worker pod template spec", func() {
					workerComp, err := pyflowFactory.GetComponent(workerComponentName)
					Expect(err).NotTo(HaveOccurred())

					result, err := pyflowAccessor.ExtractPodTemplateSpec(ctx, workerComp.definition)

					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(HaveLen(1))
					Expect(result[0].ObjectMeta.Labels[roleLabel]).To(Equal(workerRole))
					Expect(result[0].Spec.Containers[0].Name).To(Equal(trainerContainerName))
				})
			})
		})

		Context("unsupported workloads", func() {
			It("should return error for workloads without PodTemplateSpecPath", func() {
				jobComp, err := jobgroupFactory.GetComponent(jobComponentName)
				Expect(err).NotTo(HaveOccurred())

				_, err = jobgroupAccessor.ExtractPodTemplateSpec(ctx, jobComp.definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(noPodTemplateSpecError))
			})

			It("should return error for fragmented workloads", func() {
				serviceComp, err := reactorFactory.GetComponent(serviceComponentName)
				Expect(err).NotTo(HaveOccurred())

				_, err = reactorAccessor.ExtractPodTemplateSpec(ctx, serviceComp.definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(noPodTemplateSpecError))
			})
		})
	})

	Describe("ExtractPodSpec", func() {
		Context("supported workloads", func() {
			It("should extract job pod specs", func() {
				jobComp, err := jobgroupFactory.GetComponent(jobComponentName)
				Expect(err).NotTo(HaveOccurred())

				result, err := jobgroupAccessor.ExtractPodSpec(ctx, jobComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(2))
				Expect(result[0].Containers).To(HaveLen(1))
				Expect(result[0].Containers[0].Name).To(Equal(indexerContainerName))
				Expect(result[1].Containers[0].Name).To(Equal(processorContainerName))
			})
		})

		Context("unsupported workloads", func() {
			It("should return error for template-based workloads", func() {
				masterComp, err := pyflowFactory.GetComponent(masterComponentName)
				Expect(err).NotTo(HaveOccurred())

				_, err = pyflowAccessor.ExtractPodSpec(ctx, masterComp.definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(noPodSpecError))
			})

			It("should return error for fragmented workloads", func() {
				serviceComp, err := reactorFactory.GetComponent(serviceComponentName)
				Expect(err).NotTo(HaveOccurred())

				_, err = reactorAccessor.ExtractPodSpec(ctx, serviceComp.definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(noPodSpecError))
			})
		})
	})

	Describe("ExtractPodMetadata", func() {
		Context("supported workloads", func() {
			It("should extract job pod metadata", func() {
				jobComp, err := jobgroupFactory.GetComponent(jobComponentName)
				Expect(err).NotTo(HaveOccurred())

				result, err := jobgroupAccessor.ExtractPodMetadata(ctx, jobComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(2))
				Expect(result[0].Labels[roleLabel]).To(Equal(indexerRole))
				Expect(result[0].Labels[appLabel]).To(Equal(jobgroupApp))
				Expect(result[1].Labels[roleLabel]).To(Equal(processorRole))
			})
		})

		Context("unsupported workloads", func() {
			It("should return error for template-based workloads", func() {
				masterComp, err := pyflowFactory.GetComponent(masterComponentName)
				Expect(err).NotTo(HaveOccurred())

				_, err = pyflowAccessor.ExtractPodMetadata(ctx, masterComp.definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(noMetadataError))
			})

			It("should return error for fragmented workloads", func() {
				serviceComp, err := reactorFactory.GetComponent(serviceComponentName)
				Expect(err).NotTo(HaveOccurred())

				_, err = reactorAccessor.ExtractPodMetadata(ctx, serviceComp.definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(noMetadataError))
			})
		})
	})

	Describe("ExtractFragmentedPodSpec", func() {
		Context("supported workloads", func() {
			It("should extract fragmented pod specs", func() {
				serviceComp, err := reactorFactory.GetComponent(serviceComponentName)
				Expect(err).NotTo(HaveOccurred())

				result, err := reactorAccessor.ExtractFragmentedPodSpec(ctx, serviceComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(3)) // api + worker + cache services

				// Verify we have the expected services (map iteration order is non-deterministic)
				serviceNames := make([]string, len(result))
				for i, spec := range result {
					serviceNames[i] = spec.Labels[serviceNameLabel]
				}
				Expect(serviceNames).To(ConsistOf(apiServiceName, workerServiceName, cacheServiceName))

				// Find each service for detailed assertions
				var apiSpec, workerSpec, cacheSpec *FragmentedPodSpec
				for i := range result {
					switch result[i].Labels[serviceNameLabel] {
					case apiServiceName:
						apiSpec = &result[i]
					case workerServiceName:
						workerSpec = &result[i]
					case cacheServiceName:
						cacheSpec = &result[i]
					}
				}

				// API service assertions
				Expect(apiSpec).NotTo(BeNil())
				Expect(apiSpec.Labels).To(HaveKeyWithValue(appLabel, reactorApp))
				Expect(apiSpec.Labels).To(HaveKeyWithValue(serviceNameLabel, apiServiceName))
				Expect(apiSpec.Labels).To(HaveKeyWithValue("tier", "frontend"))
				Expect(apiSpec.Annotations).To(HaveKeyWithValue("service.beta.kubernetes.io/aws-load-balancer-type", "nlb"))
				Expect(apiSpec.Containers).To(HaveLen(1))
				Expect(apiSpec.Containers[0].Name).To(Equal("api-server"))
				Expect(apiSpec.Containers[0].Image).To(Equal("api:latest"))
				Expect(apiSpec.Containers[0].Ports).To(HaveLen(1))
				Expect(apiSpec.Containers[0].Ports[0].ContainerPort).To(Equal(int32(8080)))
				Expect(apiSpec.Containers[0].Env).To(ContainElement(corev1.EnvVar{Name: "PORT", Value: "8080"}))
				Expect(apiSpec.Container.Name).To(Equal("api-server-main"))
				Expect(apiSpec.Container.Image).To(Equal("api:latest"))
				Expect(apiSpec.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceCPU, resource.MustParse("500m")))
				Expect(apiSpec.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceMemory, resource.MustParse("1Gi")))
				Expect(apiSpec.Resources.Limits).To(HaveKeyWithValue(corev1.ResourceCPU, resource.MustParse("1")))
				Expect(apiSpec.Resources.Limits).To(HaveKeyWithValue(corev1.ResourceMemory, resource.MustParse("2Gi")))

				// Worker service assertions
				Expect(workerSpec).NotTo(BeNil())
				Expect(workerSpec.Labels).To(HaveKeyWithValue(appLabel, reactorApp))
				Expect(workerSpec.Labels).To(HaveKeyWithValue(serviceNameLabel, workerServiceName))
				Expect(workerSpec.Labels).To(HaveKeyWithValue("tier", "backend"))
				Expect(workerSpec.Annotations).To(HaveKeyWithValue("prometheus.io/scrape", "true"))
				Expect(workerSpec.Annotations).To(HaveKeyWithValue("prometheus.io/port", "9090"))
				Expect(workerSpec.Containers).To(HaveLen(1))
				Expect(workerSpec.Containers[0].Name).To(Equal("worker"))
				Expect(workerSpec.Containers[0].Image).To(Equal("worker:latest"))
				Expect(workerSpec.Containers[0].Env).To(ContainElement(corev1.EnvVar{Name: "WORKER_TYPE", Value: "processor"}))
				Expect(workerSpec.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceCPU, resource.MustParse("1")))
				Expect(workerSpec.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceMemory, resource.MustParse("2Gi")))
				Expect(workerSpec.Resources.Limits).To(HaveKeyWithValue(corev1.ResourceCPU, resource.MustParse("2")))
				Expect(workerSpec.Resources.Limits).To(HaveKeyWithValue(corev1.ResourceMemory, resource.MustParse("4Gi")))
				gpuResource := corev1.ResourceName("nvidia.com/gpu")
				Expect(workerSpec.Resources.Limits).To(HaveKey(gpuResource))
				Expect(workerSpec.Resources.Limits[gpuResource]).To(Equal(resource.MustParse("1")))

				// Cache service assertions
				Expect(cacheSpec).NotTo(BeNil())
				Expect(cacheSpec.Labels).To(HaveKeyWithValue(appLabel, reactorApp))
				Expect(cacheSpec.Labels).To(HaveKeyWithValue(serviceNameLabel, cacheServiceName))
				Expect(cacheSpec.Labels).To(HaveKeyWithValue("tier", "middleware"))
				// Cache service has NO annotations in test data - validates missing field handling
				Expect(cacheSpec.Annotations).To(BeEmpty())
				Expect(cacheSpec.Containers).To(HaveLen(1))
				Expect(cacheSpec.Containers[0].Name).To(Equal("redis"))
				Expect(cacheSpec.Containers[0].Image).To(Equal("redis:7-alpine"))
				Expect(cacheSpec.Containers[0].Ports).To(HaveLen(1))
				Expect(cacheSpec.Containers[0].Ports[0].ContainerPort).To(Equal(int32(6379)))
				Expect(cacheSpec.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceCPU, resource.MustParse("250m")))
				Expect(cacheSpec.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceMemory, resource.MustParse("512Mi")))
				Expect(cacheSpec.Resources.Limits).To(HaveKeyWithValue(corev1.ResourceCPU, resource.MustParse("500m")))
				Expect(cacheSpec.Resources.Limits).To(HaveKeyWithValue(corev1.ResourceMemory, resource.MustParse("1Gi")))
			})
		})

		Context("unsupported workloads", func() {
			It("should return error for template-based workloads", func() {
				masterComp, err := pyflowFactory.GetComponent(masterComponentName)
				Expect(err).NotTo(HaveOccurred())

				_, err = pyflowAccessor.ExtractFragmentedPodSpec(ctx, masterComp.definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(noFragmentedSpecError))
			})

			It("should return error for array-based workloads", func() {
				jobComp, err := jobgroupFactory.GetComponent(jobComponentName)
				Expect(err).NotTo(HaveOccurred())

				_, err = jobgroupAccessor.ExtractFragmentedPodSpec(ctx, jobComp.definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(noFragmentedSpecError))
			})

			It("should handle nil fragmented pod spec definition", func() {
				definition := v1alpha1.ComponentDefinition{
					Name: "test-component",
					SpecDefinition: &v1alpha1.SpecDefinition{
						FragmentedPodSpecDefinition: nil,
					},
				}

				_, err := reactorAccessor.ExtractFragmentedPodSpec(ctx, definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(noFragmentedSpecError))
			})

			It("should handle nil spec definition", func() {
				definition := v1alpha1.ComponentDefinition{
					Name:           "test-component",
					SpecDefinition: nil,
				}

				_, err := reactorAccessor.ExtractFragmentedPodSpec(ctx, definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(noSpecDefinitionError))
			})
		})
	})

	Describe("ExtractScale", func() {
		Context("PyFlow", func() {
			It("should extract master scale (replicas only)", func() {
				masterComp, err := pyflowFactory.GetComponent(masterComponentName)
				Expect(err).NotTo(HaveOccurred())

				result, err := pyflowAccessor.ExtractScale(ctx, masterComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(1))
				Expect(*result[0].Replicas).To(Equal(int32(1)))
				Expect(result[0].MinReplicas).To(BeNil())
				Expect(result[0].MaxReplicas).To(BeNil())
			})

			It("should extract worker scale", func() {
				workerComp, err := pyflowFactory.GetComponent(workerComponentName)
				Expect(err).NotTo(HaveOccurred())

				result, err := pyflowAccessor.ExtractScale(ctx, workerComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(1))
				Expect(result[0].Replicas).To(BeNil())
				Expect(*result[0].MinReplicas).To(Equal(int32(1)))
				Expect(*result[0].MaxReplicas).To(Equal(int32(5)))
			})
		})

		Context("JobGroup", func() {
			It("should extract job scales from array", func() {
				jobComp, err := jobgroupFactory.GetComponent(jobComponentName)
				Expect(err).NotTo(HaveOccurred())

				result, err := jobgroupAccessor.ExtractScale(ctx, jobComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(2))                   // indexer + processor
				Expect(*result[0].Replicas).To(Equal(int32(2))) // indexer has 2 replicas
				Expect(*result[1].Replicas).To(Equal(int32(3))) // processor has 3 replicas
			})

		})

		Context("Reactor", func() {
			It("should extract all service scales from map", func() {
				serviceComp, err := reactorFactory.GetComponent(serviceComponentName)
				Expect(err).NotTo(HaveOccurred())

				result, err := reactorAccessor.ExtractScale(ctx, serviceComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(3)) // api + worker + cache services

				// Extract the replica values (map iteration order is non-deterministic)
				replicaCounts := make([]int32, len(result))
				for i, scale := range result {
					replicaCounts[i] = *scale.Replicas
				}

				// Verify we got the expected replica counts from all services
				Expect(replicaCounts).To(ConsistOf(int32(3), int32(5), int32(1))) // api=3, worker=5, cache=1
			})
		})

		Context("scale values stored as annotation strings", func() {
			knativeAccessor := func(annotations map[string]any) *Accessor {
				object := &unstructured.Unstructured{Object: map[string]any{
					"apiVersion": "serving.knative.dev/v1",
					"kind":       "Service",
					"metadata":   map[string]any{"name": "inference"},
					"spec": map[string]any{
						"template": map[string]any{
							"metadata": map[string]any{"annotations": annotations},
						},
					},
				}}
				return NewAccessor(execution.NewDefaultRunner(object))
			}

			definition := v1alpha1.ComponentDefinition{
				Name: "revision",
				ScaleDefinition: &v1alpha1.ScaleDefinition{
					MinReplicasPath: ptr.To(`.spec.template.metadata.annotations["autoscaling.knative.dev/min-scale"] // 1`),
					MaxReplicasPath: ptr.To(`.spec.template.metadata.annotations["autoscaling.knative.dev/max-scale"]`),
				},
			}

			It("should parse numeric string annotations", func() {
				accessor := knativeAccessor(map[string]any{
					"autoscaling.knative.dev/min-scale": "2",
					"autoscaling.knative.dev/max-scale": "3",
				})

				result, err := accessor.ExtractScale(ctx, definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(1))
				Expect(*result[0].MinReplicas).To(Equal(int32(2)))
				Expect(*result[0].MaxReplicas).To(Equal(int32(3)))
			})

			It("should apply the jq fallback when the annotation is missing", func() {
				accessor := knativeAccessor(map[string]any{})

				result, err := accessor.ExtractScale(ctx, definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(1))
				Expect(*result[0].MinReplicas).To(Equal(int32(1)))
				Expect(result[0].MaxReplicas).To(BeNil())
			})

			It("should return a clear error for a non-numeric string", func() {
				accessor := knativeAccessor(map[string]any{
					"autoscaling.knative.dev/min-scale": "abc",
				})

				_, err := accessor.ExtractScale(ctx, definition)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(`replicas value "abc"`))
			})
		})

		Context("unsupported workloads", func() {
			It("should handle nil scale definition", func() {
				definition := v1alpha1.ComponentDefinition{
					Name:            "test-component",
					ScaleDefinition: nil,
				}

				_, err := reactorAccessor.ExtractScale(ctx, definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(noScaleError))
			})
		})
	})

	Describe("Error Handling", func() {
		Context("safeConvertSlice", func() {
			It("should handle conversion errors gracefully", func() {
				// Create a mock execution that returns data that can't be converted
				mockRunner := execution.NewMockRunner(gomock.NewController(GinkgoT()))
				accessor := NewAccessor(mockRunner)

				// Test with incompatible data types that should fail conversion
				mockRunner.EXPECT().
					Evaluate(gomock.Any(), "spec.podTemplate").
					Return([]any{
						map[string]any{
							"metadata": "this is a string, not ObjectMeta",
							"spec":     "this is also a string, not PodSpec",
						},
					}, nil)

				definition := v1alpha1.ComponentDefinition{
					Name: "test-component",
					SpecDefinition: &v1alpha1.SpecDefinition{
						PodTemplateSpecPath: ptr.To("spec.podTemplate"),
					},
				}

				_, err := accessor.ExtractPodTemplateSpec(ctx, definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to convert object"))
			})

			It("should handle circular reference errors in JSON conversion", func() {
				// Create a mock execution that returns circular reference data
				mockRunner := execution.NewMockRunner(gomock.NewController(GinkgoT()))
				accessor := NewAccessor(mockRunner)

				// Create a circular reference that would break JSON marshaling
				circularData := make(map[string]any)
				circularData["self"] = circularData

				mockRunner.EXPECT().
					Evaluate(gomock.Any(), "spec.resources").
					Return([]any{circularData}, nil)

				definition := v1alpha1.ComponentDefinition{
					Name: "test-component",
					SpecDefinition: &v1alpha1.SpecDefinition{
						FragmentedPodSpecDefinition: &v1alpha1.FragmentedPodSpecDefinition{
							ResourcesPath: ptr.To("spec.resources"),
						},
					},
				}

				_, err := accessor.ExtractFragmentedPodSpec(ctx, definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to convert object"))
			})
		})
	})

	Describe("ExtractInstanceIds", func() {
		Context("JobGroup", func() {
			It("should extract job instance IDs from array", func() {
				jobComp, err := jobgroupFactory.GetComponent(jobComponentName)
				Expect(err).NotTo(HaveOccurred())

				result, err := jobgroupAccessor.ExtractInstanceIds(ctx, jobComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal([]string{"indexer", "processor"}))
			})
		})

		Context("Reactor", func() {
			It("should extract service instance IDs from map keys", func() {
				serviceComp, err := reactorFactory.GetComponent(serviceComponentName)
				Expect(err).NotTo(HaveOccurred())

				result, err := reactorAccessor.ExtractInstanceIds(ctx, serviceComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(ConsistOf("api", "worker", "cache"))
			})
		})

		Context("PyFlow", func() {
			It("should return DefinitionNotFoundError for components without instance ID path", func() {
				masterComp, err := pyflowFactory.GetComponent(masterComponentName)
				Expect(err).NotTo(HaveOccurred())

				result, err := pyflowAccessor.ExtractInstanceIds(ctx, masterComp.definition)
				Expect(err).To(HaveOccurred())
				Expect(result).To(BeNil())

				var defNotFoundErr DefinitionNotFoundError
				Expect(errors.As(err, &defNotFoundErr)).To(BeTrue())
				Expect(string(defNotFoundErr)).To(ContainSubstring("no instance id path defined"))
			})
		})

		Context("validation errors", func() {
			It("should return error when instance ids contains empty strings", func() {
				jobgroupObject := types.NewJobGroupObject()
				jobgroupObject.Spec.ReplicatedJobs[0].Name = ""

				factory := NewComponentFactoryFromObject(jobgroupKarta, jobgroupObject)

				accessor := NewAccessor(execution.NewDefaultRunner(jobgroupObject))

				comp, err := factory.GetComponent("job")
				Expect(err).NotTo(HaveOccurred())

				result, err := accessor.ExtractInstanceIds(ctx, comp.definition)
				Expect(err).To(HaveOccurred())
				Expect(result).To(BeNil())
				Expect(err.Error()).To(ContainSubstring("instance id path contained empty string values"))
				Expect(err.Error()).To(ContainSubstring("[,processor]"))
			})
		})
	})

	Describe("ExtractStatus", func() {
		Context("Conditions extraction", func() {
			It("should extract status with conditions", func() {
				pyflowObject := types.NewPyFlowObject()
				pyflowObject.Status.Conditions[0] = metav1.Condition{
					Type:   "Running",
					Status: metav1.ConditionTrue,
				}
				accessor, pyflowComp := accessorForObject(pyflowKarta, pyflowObject, "pyflow")

				result, err := accessor.ExtractStatus(ctx, pyflowComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Phase).To(BeNil())
				Expect(result.Conditions).To(HaveLen(1))
				Expect(result.Conditions[0].Type).To(Equal("Running"))
				Expect(result.Conditions[0].Status).To(Equal(ptr.To("True")))
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.RunningStatus))
			})

			It("should return UndefinedStatus when conditions do not match", func() {
				pyflowObject := types.NewPyFlowObject()
				pyflowObject.Status.Conditions[0] = metav1.Condition{
					Type:   "NotMatching",
					Status: metav1.ConditionTrue,
				}
				accessor, pyflowComp := accessorForObject(pyflowKarta, pyflowObject, "pyflow")

				result, err := accessor.ExtractStatus(ctx, pyflowComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.UndefinedStatus))
			})

			It("should handle empty conditions array", func() {
				pyflowObject := types.NewPyFlowObject()
				pyflowObject.Status.Conditions = []metav1.Condition{}

				accessor, pyflowComp := accessorForObject(pyflowKarta, pyflowObject, "pyflow")

				result, err := accessor.ExtractStatus(ctx, pyflowComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Conditions).To(BeEmpty())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.UndefinedStatus))
			})

			It("should extract conditions with message field", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Conditions = []metav1.Condition{
					{
						Type:    "Failed",
						Status:  metav1.ConditionTrue,
						Message: "Pod failed due to OOMKilled",
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Conditions).To(HaveLen(1))
				Expect(result.Conditions[0].Type).To(Equal("Failed"))
				Expect(result.Conditions[0].Status).To(Equal(ptr.To("True")))
				Expect(result.Conditions[0].Message).To(Equal("Pod failed due to OOMKilled"))
			})

			It("should extract conditions with reason field", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Conditions = []metav1.Condition{
					{
						Type:    "Failed",
						Status:  metav1.ConditionTrue,
						Reason:  "OOMKilled",
						Message: "Pod failed due to OOMKilled",
					},
				}
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.ConditionsDefinition.ReasonFieldName = ptr.To("reason")
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Conditions).To(HaveLen(1))
				Expect(result.Conditions[0].Type).To(Equal("Failed"))
				Expect(result.Conditions[0].Status).To(Equal(ptr.To("True")))
				Expect(result.Conditions[0].Reason).To(Equal(ptr.To("OOMKilled")))
				Expect(result.Conditions[0].Message).To(Equal("Pod failed due to OOMKilled"))
			})
		})

		Context("Phase extraction", func() {
			It("should extract phase from status", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Phase).NotTo(BeNil())
				Expect(*result.Phase).To(Equal("running"))
			})

			It("should handle conditions without message field", func() {
				pyflowComp, err := pyflowFactory.GetComponent("pyflow")
				Expect(err).NotTo(HaveOccurred())

				result, err := pyflowAccessor.ExtractStatus(ctx, pyflowComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Conditions).To(HaveLen(1))
				Expect(result.Conditions[0].Message).To(BeEmpty())
			})

			It("should handle phase missing", func() {
				pyflowComp, err := pyflowFactory.GetComponent("pyflow")
				Expect(err).NotTo(HaveOccurred())

				result, err := pyflowAccessor.ExtractStatus(ctx, pyflowComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Phase).To(BeNil())
			})

			It("should match when ANY matcher succeeds with OR logic", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "active"
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Running: []v1alpha1.StatusMatcher{
						{ByPhase: "running"},
						{ByPhase: "active"},
						{ByConditions: []v1alpha1.ExpectedCondition{
							{Type: "Ready", Status: ptr.To("True")},
						}},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.RunningStatus))
			})
		})
		Context("Phase matching", func() {
			It("should extract status with phase and match Running", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Phase).NotTo(BeNil())
				Expect(*result.Phase).To(Equal("running"))
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.RunningStatus))
			})

			It("should match Initializing status", func() {
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Initializing: []v1alpha1.StatusMatcher{
						{
							ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Initialized", Status: ptr.To("True")},
							},
						},
					},
				}
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Initialized", Status: metav1.ConditionTrue},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.InitializingStatus))
			})

			It("should match Running status", func() {
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Running: []v1alpha1.StatusMatcher{
						{
							ByPhase: "running",
						},
					},
				}
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.RunningStatus))
			})

			It("should match Failed status", func() {
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Failed: []v1alpha1.StatusMatcher{
						{
							ByPhase: "failed",
						},
					},
				}
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "failed"
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.FailedStatus))
			})

			It("should match Completed status", func() {
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Completed: []v1alpha1.StatusMatcher{
						{
							ByPhase: "completed",
						},
					},
				}
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "completed"
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.CompletedStatus))
			})

			It("should match Degraded status", func() {
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Degraded: []v1alpha1.StatusMatcher{
						{
							ByPhase: "degraded",
						},
					},
				}
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "degraded"
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.DegradedStatus))
			})

			It("should match Suspended status", func() {
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Suspended: []v1alpha1.StatusMatcher{
						{
							ByPhase: "suspended",
						},
					},
				}
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "suspended"
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.SuspendedStatus))
			})

			It("should match Suspending status", func() {
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Suspending: []v1alpha1.StatusMatcher{
						{
							ByPhase: "suspending",
						},
					},
				}
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "suspending"
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.SuspendingStatus))
			})

			It("should match Resuming status", func() {
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Resuming: []v1alpha1.StatusMatcher{
						{
							ByPhase: "resuming",
						},
					},
				}
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "resuming"
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.ResumingStatus))
			})

			It("should return UndefinedStatus when phase does not match", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "unknown"

				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.UndefinedStatus))
			})

			It("should match status with both byPhase and byConditions", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionTrue},
					{Type: "Available", Status: metav1.ConditionFalse},
				}
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Running: []v1alpha1.StatusMatcher{
						{
							ByPhase: "running",
							ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Ready", Status: ptr.To("True")},
								{Type: "Available", Status: ptr.To("False")},
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.RunningStatus))
			})

			It("should fail to match when phase matches but conditions do not", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionFalse},
				}
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Running: []v1alpha1.StatusMatcher{
						{
							ByPhase: "running",
							ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Ready", Status: ptr.To("True")},
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.UndefinedStatus))
			})

			It("should match multiple statuses when overlapping matchers succeed", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionTrue},
				}
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Running: []v1alpha1.StatusMatcher{
						{
							ByPhase: "running",
						},
					},
					Initializing: []v1alpha1.StatusMatcher{
						{
							ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Ready", Status: ptr.To("True")},
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.RunningStatus, v1alpha1.InitializingStatus))
			})

			It("should not match when condition is missing", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionTrue},
				}
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Running: []v1alpha1.StatusMatcher{
						{
							ByPhase: "running",
							ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Ready", Status: ptr.To("True")},
								{Type: "Failed", Status: ptr.To("False")},
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.UndefinedStatus))
			})

			It("should match when reason field matches", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Failed", Status: metav1.ConditionTrue, Reason: "OOMKilled"},
				}
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.ConditionsDefinition.ReasonFieldName = ptr.To("reason")
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Failed: []v1alpha1.StatusMatcher{
						{
							ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Failed", Status: ptr.To("True"), Reason: ptr.To("OOMKilled")},
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.FailedStatus))
			})

			It("should not match when reason field does not match", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Failed", Status: metav1.ConditionTrue, Reason: "CrashLoopBackOff"},
				}
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.ConditionsDefinition.ReasonFieldName = ptr.To("reason")
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Failed: []v1alpha1.StatusMatcher{
						{
							ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Failed", Status: ptr.To("True"), Reason: ptr.To("OOMKilled")},
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.UndefinedStatus))
			})

			It("should not match when reason is expected but condition has no reason", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Failed", Status: metav1.ConditionTrue},
				}
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.ConditionsDefinition.ReasonFieldName = ptr.To("reason")
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Failed: []v1alpha1.StatusMatcher{
						{
							ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Failed", Status: ptr.To("True"), Reason: ptr.To("OOMKilled")},
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.UndefinedStatus))
			})

			It("should match when no reason is expected and condition has a reason", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Failed", Status: metav1.ConditionTrue, Reason: "OOMKilled"},
				}
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.ConditionsDefinition.ReasonFieldName = ptr.To("reason")
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Failed: []v1alpha1.StatusMatcher{
						{
							ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Failed", Status: ptr.To("True")},
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.FailedStatus))
			})

			It("should match with multiple conditions including reason", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionFalse},
					{Type: "Failed", Status: metav1.ConditionTrue, Reason: "OOMKilled"},
				}
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.ConditionsDefinition.ReasonFieldName = ptr.To("reason")
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Failed: []v1alpha1.StatusMatcher{
						{
							ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Ready", Status: ptr.To("False")},
								{Type: "Failed", Reason: ptr.To("OOMKilled")},
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.FailedStatus))
			})

			It("should match status with ByExpression returning string 'running'", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Running: []v1alpha1.StatusMatcher{
						{
							ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     `.status.phase`,
								ExpectedResult: "running",
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.RunningStatus))
			})

			It("should not match when ByExpression returns false", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Failed: []v1alpha1.StatusMatcher{
						{
							ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     `.status.phase == "failed"`,
								ExpectedResult: "true",
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.UndefinedStatus))
			})

			It("should convert the byExpression result to a string and compare it to the expected value", func() {
				cases := []struct {
					name     string
					expr     string
					expected string
					match    bool
				}{
					{"string result matches", `.status.phase`, "running", true},
					{"boolean true becomes the string true", `.status.phase == "running"`, "true", true},
					{"boolean false becomes the string false", `.status.phase == "failed"`, "false", true},
					{"number becomes its digits", `.status.conditions | length`, "1", true},
					{"a different string does not match", `.status.phase`, "stopped", false},
					{"a value with a quote is compared literally", `.status.phase`, `running "now"`, false},
				}
				for _, c := range cases {
					reactorObject := types.NewReactorObject()
					reactorObject.Status.Phase = "running"
					reactorObject.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
					reactorKarta := types.ReactorKarta()
					reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
						Running: []v1alpha1.StatusMatcher{
							{
								ByExpression: &v1alpha1.ExpressionMatcher{
									Expression:     c.expr,
									ExpectedResult: c.expected,
								},
							},
						},
					}
					accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

					result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

					Expect(err).NotTo(HaveOccurred(), c.name)
					Expect(result).NotTo(BeNil(), c.name)
					if c.match {
						Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.RunningStatus), c.name)
					} else {
						Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.UndefinedStatus), c.name)
					}
				}
			})

			It("should match status with ByExpression checking complex conditions", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionTrue},
				}
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Running: []v1alpha1.StatusMatcher{
						{
							ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     `.status.phase == "running" and (.status.conditions[] | select(.type == "Ready") | .status == "True")`,
								ExpectedResult: "true",
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.RunningStatus))
			})

			It("should match with both ByPhase and ByExpression (AND logic)", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionTrue},
				}
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Running: []v1alpha1.StatusMatcher{
						{
							ByPhase: "running",
							ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     `.status.conditions | length > 0`,
								ExpectedResult: "true",
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.RunningStatus))
			})

			It("should not match when ByPhase matches but ByExpression does not", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				reactorObject.Status.Conditions = []metav1.Condition{}
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Running: []v1alpha1.StatusMatcher{
						{
							ByPhase: "running",
							ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     `.status.conditions | length > 0`,
								ExpectedResult: "true",
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.UndefinedStatus))
			})

			It("should match with ByPhase, ByConditions, and ByExpression (all AND)", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionTrue},
				}
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Running: []v1alpha1.StatusMatcher{
						{
							ByPhase: "running",
							ByConditions: []v1alpha1.ExpectedCondition{
								{Type: "Ready", Status: ptr.To("True")},
							},
							ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     `.status.conditions | length > 0`,
								ExpectedResult: "true",
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.RunningStatus))
			})

			It("should not match when ByExpression returns null", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Running: []v1alpha1.StatusMatcher{
						{
							ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     `.status.nonExistentField`,
								ExpectedResult: "true",
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.UndefinedStatus))
			})

			It("should return error when ByExpression is invalid", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Phase = "running"
				reactorKarta := types.ReactorKarta()
				reactorKarta.Spec.StructureDefinition.RootComponent.StatusDefinition.StatusMappings = v1alpha1.StatusMappings{
					Running: []v1alpha1.StatusMatcher{
						{
							ByExpression: &v1alpha1.ExpressionMatcher{
								Expression:     `.status.phase ==`,
								ExpectedResult: "true",
							},
						},
					},
				}
				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to match status"))
				Expect(result).To(BeNil())
			})
		})

		Context("Completed status", func() {
			It("should match Completed status", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Completed", Status: metav1.ConditionTrue},
				}

				customKarta := reactorKarta
				customKarta.Spec.StructureDefinition.RootComponent.StatusDefinition = &v1alpha1.StatusDefinition{
					ConditionsDefinition: &v1alpha1.ConditionsDefinition{
						Path:            ".status.conditions",
						TypeFieldName:   "type",
						StatusFieldName: "status",
					},
					StatusMappings: v1alpha1.StatusMappings{
						Completed: []v1alpha1.StatusMatcher{
							{
								ByConditions: []v1alpha1.ExpectedCondition{
									{Type: "Completed", Status: ptr.To("True")},
								},
							},
						},
					},
				}

				accessor, reactorComp := accessorForObject(customKarta, reactorObject, "reactor")

				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.MatchedStatuses).To(ConsistOf(v1alpha1.CompletedStatus))
			})
		})

		Context("error handling", func() {
			It("should return DefinitionNotFoundError when StatusDefinition is nil", func() {
				definition := v1alpha1.ComponentDefinition{
					Name:             "test-component",
					StatusDefinition: nil,
				}

				_, err := pyflowAccessor.ExtractStatus(ctx, definition)

				Expect(err).To(HaveOccurred())
				var defNotFoundErr DefinitionNotFoundError
				Expect(errors.As(err, &defNotFoundErr)).To(BeTrue())
				Expect(string(defNotFoundErr)).To(ContainSubstring("does not have status definition"))
			})

			It("should handle invalid phase path", func() {
				mockRunner := execution.NewMockRunner(gomock.NewController(GinkgoT()))
				accessor := NewAccessor(mockRunner)

				mockRunner.EXPECT().
					Evaluate(gomock.Any(), ".status.invalidPath").
					Return(nil, errors.New("query evaluation failed"))

				definition := v1alpha1.ComponentDefinition{
					Name: "test-component",
					StatusDefinition: &v1alpha1.StatusDefinition{
						PhaseDefinition: &v1alpha1.PhaseDefinition{
							Path: ".status.invalidPath",
						},
						StatusMappings: v1alpha1.StatusMappings{},
					},
				}

				_, err := accessor.ExtractStatus(ctx, definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to extract phase"))
			})

			It("should handle invalid conditions path", func() {
				definition := v1alpha1.ComponentDefinition{
					Name: "test-component",
					StatusDefinition: &v1alpha1.StatusDefinition{
						ConditionsDefinition: &v1alpha1.ConditionsDefinition{
							Path:            ".status.\\.badConditions",
							TypeFieldName:   "type",
							StatusFieldName: "status",
						},
						StatusMappings: v1alpha1.StatusMappings{},
					},
				}

				_, err := pyflowAccessor.ExtractStatus(ctx, definition)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failed to extract conditions"))
			})

			It("should handle missing condition fields gracefully", func() {
				reactorObject := types.NewReactorObject()
				reactorObject.Status.Conditions = []metav1.Condition{
					{Type: "Ready"},
				}

				accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")
				result, err := accessor.ExtractStatus(ctx, reactorComp.definition)

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Conditions).To(HaveLen(1))
				Expect(result.Conditions[0].Type).To(Equal("Ready"))
				Expect(*result.Conditions[0].Status).To(BeEmpty())
			})
		})
	})
	Describe("UpdatePodSpec", func() {
		// JobGroup Karta has PodSpecPath
		It("should update pod spec with instance Ids", func() {
			jobgroupObject := types.NewJobGroupObject()
			jobgroupKarta := types.JobGroupKarta()
			accessor, jobgroupComp := accessorForObject(jobgroupKarta, jobgroupObject, "job")
			currentPodSpecs, err := accessor.ExtractPodSpec(ctx, jobgroupComp.definition)
			Expect(err).NotTo(HaveOccurred())

			for i := range currentPodSpecs {
				currentPodSpecs[i].Containers = []corev1.Container{{Name: "updated-container-" + strconv.Itoa(i), Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: "updated-resource-claim-" + strconv.Itoa(i)}}}}}
				currentPodSpecs[i].SchedulerName = "updated-scheduler-" + strconv.Itoa(i)
			}
			err = accessor.UpdatePodSpec(ctx, jobgroupComp.definition, currentPodSpecs)
			Expect(err).NotTo(HaveOccurred())

			updatedObject, err := accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			updatedJobgroupObject := types.JobGroup{}
			err = convertViaJSON(updatedObject, &updatedJobgroupObject)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[0].Spec.Containers).To(HaveLen(1))
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[0].Spec.Containers[0].Name).To(Equal("updated-container-0"))
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[0].Spec.Containers[0].Resources.Claims).To(HaveLen(1))
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[0].Spec.Containers[0].Resources.Claims[0].Name).To(Equal("updated-resource-claim-0"))
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[0].Spec.SchedulerName).To(Equal("updated-scheduler-0"))
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[1].Spec.Containers).To(HaveLen(1))
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[1].Spec.Containers[0].Name).To(Equal("updated-container-1"))
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[1].Spec.Containers[0].Resources.Claims).To(HaveLen(1))
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[1].Spec.Containers[0].Resources.Claims[0].Name).To(Equal("updated-resource-claim-1"))
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[1].Spec.SchedulerName).To(Equal("updated-scheduler-1"))
		})
		It("should remove fields that are not present in the updated pod specs", func() {
			jobgroupObject := types.NewJobGroupObject()
			jobgroupKarta := types.JobGroupKarta()
			accessor, jobgroupComp := accessorForObject(jobgroupKarta, jobgroupObject, "job")
			currentPodSpecs, err := accessor.ExtractPodSpec(ctx, jobgroupComp.definition)
			Expect(err).NotTo(HaveOccurred())

			for i := range currentPodSpecs {
				currentPodSpecs[i].Containers = []corev1.Container{}
			}
			err = accessor.UpdatePodSpec(ctx, jobgroupComp.definition, currentPodSpecs)
			Expect(err).NotTo(HaveOccurred())

			updatedObject, err := accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			updatedJobgroupObject := types.JobGroup{}
			err = convertViaJSON(updatedObject, &updatedJobgroupObject)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[0].Spec.Containers).To(BeEmpty())
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[1].Spec.Containers).To(BeEmpty())

		})

		It("should return error for Karta without PodSpecPath", func() {
			pyflowObject := types.NewPyFlowObject()
			// PyFlow does not have PodSpecPath
			pyflowKarta := types.PyFlowKarta()
			accessor, masterComp := accessorForObject(pyflowKarta, pyflowObject, "master")

			podSpecs := []corev1.PodSpec{{
				Containers: []corev1.Container{{Name: "test-container"}},
			}}
			err := accessor.UpdatePodSpec(ctx, masterComp.definition, podSpecs)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(noPodSpecError))
		})
	})

	Describe("UpdateFragmentedPodSpec", func() {
		// Reactor Karta has FragmentedPodSpecDefinition
		It("should update pod spec with fragmented pod spec", func() {
			reactorObject := types.NewReactorObject()
			reactorKarta := types.ReactorKarta()
			accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "service")
			currentFragmentedPodSpecs, err := accessor.ExtractFragmentedPodSpec(ctx, reactorComp.definition)
			Expect(err).NotTo(HaveOccurred())

			for i := range currentFragmentedPodSpecs {
				currentFragmentedPodSpecs[i].Containers = []corev1.Container{{Name: "updated-container-" + strconv.Itoa(i)}}
				currentFragmentedPodSpecs[i].Labels = map[string]string{"updated": "true"}
				currentFragmentedPodSpecs[i].Annotations = map[string]string{"updated": "true"}
				currentFragmentedPodSpecs[i].Resources = &corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: "updated-resource-claim-" + strconv.Itoa(i)}}}
			}
			err = accessor.UpdateFragmentedPodSpec(ctx, reactorComp.definition, currentFragmentedPodSpecs)
			Expect(err).NotTo(HaveOccurred())

			instanceIds, err := reactorComp.GetInstanceIds(ctx)
			Expect(err).NotTo(HaveOccurred())
			updatedObject, err := accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			updatedReactorObject := types.Reactor{}
			err = convertViaJSON(updatedObject, &updatedReactorObject)
			Expect(err).NotTo(HaveOccurred())
			for i, instanceId := range instanceIds {
				Expect(updatedReactorObject.Spec.Services[instanceId].Containers).To(HaveLen(1))
				Expect(updatedReactorObject.Spec.Services[instanceId].Containers[0].Name).To(Equal("updated-container-" + strconv.Itoa(i)))
				Expect(updatedReactorObject.Spec.Services[instanceId].Resources.Claims).To(HaveLen(1))
				Expect(updatedReactorObject.Spec.Services[instanceId].Resources.Claims[0].Name).To(Equal("updated-resource-claim-" + strconv.Itoa(i)))
				Expect(updatedReactorObject.Spec.Services[instanceId].Labels).To(HaveKeyWithValue("updated", "true"))
				Expect(updatedReactorObject.Spec.Services[instanceId].Annotations).To(HaveKeyWithValue("updated", "true"))
			}
		})

		It("should update containers and verify other fields remain unchanged", func() {
			reactorObject := types.NewReactorObject()
			reactorKarta := types.ReactorKarta()
			reactorObject.Labels = map[string]string{"updated": "true"}
			accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "service")
			currentFragmentedPodSpecs, err := accessor.ExtractFragmentedPodSpec(ctx, reactorComp.definition)
			Expect(err).NotTo(HaveOccurred())

			// Store original labels to verify they remain unchanged
			originalLabels := make([]map[string]string, len(currentFragmentedPodSpecs))
			for i := range currentFragmentedPodSpecs {
				originalLabels[i] = make(map[string]string)
				for k, v := range currentFragmentedPodSpecs[i].Labels {
					originalLabels[i][k] = v
				}
				currentFragmentedPodSpecs[i].Containers = []corev1.Container{{
					Name:  "updated-container-" + strconv.Itoa(i),
					Image: "updated-image:" + strconv.Itoa(i),
				}}
			}
			err = accessor.UpdateFragmentedPodSpec(ctx, reactorComp.definition, currentFragmentedPodSpecs)
			Expect(err).NotTo(HaveOccurred())

			instanceIds, err := reactorComp.GetInstanceIds(ctx)
			Expect(err).NotTo(HaveOccurred())
			updatedObject, err := accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			updatedReactorObject := types.Reactor{}
			err = convertViaJSON(updatedObject, &updatedReactorObject)
			Expect(err).NotTo(HaveOccurred())

			for i, instanceId := range instanceIds {
				// Verify containers were updated
				Expect(updatedReactorObject.Spec.Services[instanceId].Containers).To(HaveLen(1))
				Expect(updatedReactorObject.Spec.Services[instanceId].Containers[0].Name).To(Equal("updated-container-" + strconv.Itoa(i)))
				Expect(updatedReactorObject.Spec.Services[instanceId].Containers[0].Image).To(Equal("updated-image:" + strconv.Itoa(i)))
				// Verify labels remain unchanged (not part of UpdateFragmentedPodSpec)
				for k, v := range originalLabels[i] {
					Expect(updatedReactorObject.Spec.Services[instanceId].Labels).To(HaveKeyWithValue(k, v))
				}
			}
		})

		It("should return error for Karta without FragmentedPodSpecDefinition", func() {
			pyflowObject := types.NewPyFlowObject()
			pyflowKarta := types.PyFlowKarta()
			accessor, masterComp := accessorForObject(pyflowKarta, pyflowObject, "master")

			fragmentedSpecs := []FragmentedPodSpec{{
				Containers: []corev1.Container{{Name: "test-container"}},
			}}

			err := accessor.UpdateFragmentedPodSpec(ctx, masterComp.definition, fragmentedSpecs)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(noFragmentedSpecError))
		})

		// Reproduces the Dynamo bug: services without labels/annotations/resources get null after mutation.
		// K8s rejects: "spec.services.VllmDecodeWorker.labels: Invalid value: "null": must be of type object"
		//
		// The Reactor Karta defines jq paths for labels, annotations, and resources
		// (via FragmentedPodSpecDefinition). When the source object doesn't have these
		// fields, jq evaluates them to null. After extract → update round-trip,
		// these nulls get written back into the object JSON, which K8s rejects.
		It("should not produce null values for nil fields after extract-update round-trip", func() {
			// Both services intentionally omit labels, annotations, and resources.
			// The Karta has labelsPath, annotationsPath, and resourcesPath defined,
			// so the round-trip will attempt to write nil values back for these fields.
			reactorObject := &types.Reactor{
				TypeMeta:   metav1.TypeMeta{APIVersion: "jobs.example.com/v1", Kind: "Reactor"},
				ObjectMeta: metav1.ObjectMeta{Name: "test-nil-fields", Namespace: "default"},
				Spec: types.ReactorSpec{
					Services: map[string]types.ServiceSpec{
						"frontend": {
							// No labels, annotations, or resources
							Containers:    []corev1.Container{{Name: "frontend", Image: "frontend:latest"}},
							MainContainer: corev1.Container{Name: "frontend-main", Image: "frontend:latest"},
							Replicas:      1,
						},
						"worker": {
							// No labels, annotations, or resources
							Containers:    []corev1.Container{{Name: "worker", Image: "worker:latest"}},
							MainContainer: corev1.Container{Name: "worker-main", Image: "worker:latest"},
							Replicas:      1,
						},
					},
				},
			}

			reactorKarta := types.ReactorKarta()
			accessor, serviceComp := accessorForObject(reactorKarta, reactorObject, "service")

			specs, err := accessor.ExtractFragmentedPodSpec(ctx, serviceComp.definition)
			Expect(err).NotTo(HaveOccurred())
			err = accessor.UpdateFragmentedPodSpec(ctx, serviceComp.definition, specs)
			Expect(err).NotTo(HaveOccurred())

			updatedObject, err := accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			jsonBytes, err := json.Marshal(updatedObject)
			Expect(err).NotTo(HaveOccurred())

			var result map[string]interface{}
			Expect(json.Unmarshal(jsonBytes, &result)).To(Succeed())

			services := result["spec"].(map[string]interface{})["services"].(map[string]interface{})
			for serviceName, svcRaw := range services {
				service := svcRaw.(map[string]interface{})
				for _, field := range []string{"labels", "annotations", "resources"} {
					val, present := service[field]
					if present {
						Expect(val).NotTo(BeNil(),
							"service %s: %s is null after round-trip (K8s rejects null for object-type fields)",
							serviceName, field)
					}
				}
			}
		})
	})

	Describe("UpdatePodTemplateSpec", func() {
		// PyFlow Karta has PodTemplateSpecPath
		It("should update master pod template spec with resource claims", func() {
			pyflowObject := types.NewPyFlowObject()
			pyflowKarta := types.PyFlowKarta()
			accessor, masterComp := accessorForObject(pyflowKarta, pyflowObject, "master")

			currentPodTemplateSpecs, err := accessor.ExtractPodTemplateSpec(ctx, masterComp.definition)
			Expect(err).NotTo(HaveOccurred())
			Expect(currentPodTemplateSpecs).To(HaveLen(1))

			currentPodTemplateSpecs[0].Spec.ResourceClaims = []corev1.PodResourceClaim{
				{
					Name: "test-claim",
				},
			}

			err = accessor.UpdatePodTemplateSpec(ctx, masterComp.definition, currentPodTemplateSpecs)
			Expect(err).NotTo(HaveOccurred())

			updatedObject, err := accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			updatedPyflowObject := types.PyFlow{}
			err = convertViaJSON(updatedObject, &updatedPyflowObject)
			Expect(err).NotTo(HaveOccurred())

			Expect(updatedPyflowObject.Spec.Master.Template.Spec.ResourceClaims).To(HaveLen(1))
			Expect(updatedPyflowObject.Spec.Master.Template.Spec.ResourceClaims[0].Name).To(Equal("test-claim"))
		})

		It("should update worker pod template spec", func() {
			pyflowObject := types.NewPyFlowObject()
			pyflowKarta := types.PyFlowKarta()
			accessor, workerComp := accessorForObject(pyflowKarta, pyflowObject, "worker")

			currentPodTemplateSpecs, err := accessor.ExtractPodTemplateSpec(ctx, workerComp.definition)
			Expect(err).NotTo(HaveOccurred())
			Expect(currentPodTemplateSpecs).To(HaveLen(1))

			// Modify labels and add resource claims
			currentPodTemplateSpecs[0].Labels["updated"] = "true"
			currentPodTemplateSpecs[0].Spec.ResourceClaims = []corev1.PodResourceClaim{
				{
					Name: "worker-claim",
				},
			}

			err = accessor.UpdatePodTemplateSpec(ctx, workerComp.definition, currentPodTemplateSpecs)
			Expect(err).NotTo(HaveOccurred())

			updatedObject, err := accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			updatedPyflowObject := types.PyFlow{}
			err = convertViaJSON(updatedObject, &updatedPyflowObject)
			Expect(err).NotTo(HaveOccurred())

			Expect(updatedPyflowObject.Spec.Worker.Template.ObjectMeta.Labels).To(HaveKeyWithValue("updated", "true"))
			Expect(updatedPyflowObject.Spec.Worker.Template.Spec.ResourceClaims).To(HaveLen(1))
			Expect(updatedPyflowObject.Spec.Worker.Template.Spec.ResourceClaims[0].Name).To(Equal("worker-claim"))
		})

		It("should return error for workloads without PodTemplateSpecPath", func() {
			jobgroupObject := types.NewJobGroupObject()
			// JobGroup does not have PodTemplateSpecPath
			jobgroupKarta := types.JobGroupKarta()
			accessor, jobComp := accessorForObject(jobgroupKarta, jobgroupObject, "job")

			podTemplateSpecs := []corev1.PodTemplateSpec{{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "test-container"}},
				},
			}}

			err := accessor.UpdatePodTemplateSpec(ctx, jobComp.definition, podTemplateSpecs)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(noPodTemplateSpecError))
		})
	})

	Describe("UpdatePodMetadata", func() {
		// JobGroup Karta has MetadataPath
		It("should update pod metadata with instance Ids", func() {
			jobgroupObject := types.NewJobGroupObject()
			jobgroupKarta := types.JobGroupKarta()
			jobgroupObject.Spec.ReplicatedJobs[0].Metadata.Labels = map[string]string{"current": "true"}
			jobgroupObject.Spec.ReplicatedJobs[1].Metadata.Labels = map[string]string{"current": "true"}
			accessor, jobgroupComp := accessorForObject(jobgroupKarta, jobgroupObject, "job")

			currentPodMetadata, err := accessor.ExtractPodMetadata(ctx, jobgroupComp.definition)
			Expect(err).NotTo(HaveOccurred())
			Expect(currentPodMetadata).To(HaveLen(2))

			for i := range currentPodMetadata {
				currentPodMetadata[i].Labels["updated"] = "true"
				currentPodMetadata[i].Labels["index"] = strconv.Itoa(i)
				currentPodMetadata[i].Annotations = map[string]string{
					"update-test": "value-" + strconv.Itoa(i),
				}
			}

			err = accessor.UpdatePodMetadata(ctx, jobgroupComp.definition, currentPodMetadata)
			Expect(err).NotTo(HaveOccurred())

			updatedObject, err := accessor.GetObject()
			Expect(err).NotTo(HaveOccurred())
			updatedJobgroupObject := types.JobGroup{}
			err = convertViaJSON(updatedObject, &updatedJobgroupObject)
			Expect(err).NotTo(HaveOccurred())

			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[0].Metadata.Labels).To(HaveKeyWithValue("updated", "true"))
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[0].Metadata.Labels).To(HaveKeyWithValue("index", "0"))
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[0].Metadata.Annotations).To(HaveKeyWithValue("update-test", "value-0"))
			// Current labels should remain unchanged
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[0].Metadata.Labels).To(HaveKeyWithValue("current", "true"))

			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[1].Metadata.Labels).To(HaveKeyWithValue("updated", "true"))
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[1].Metadata.Labels).To(HaveKeyWithValue("index", "1"))
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[1].Metadata.Annotations).To(HaveKeyWithValue("update-test", "value-1"))
			// Current labels should remain unchanged
			Expect(updatedJobgroupObject.Spec.ReplicatedJobs[1].Metadata.Labels).To(HaveKeyWithValue("current", "true"))
		})

		It("should return error for workloads without MetadataPath", func() {
			pyflowObject := types.NewPyFlowObject()
			// PyFlow does not have MetadataPath
			pyflowKarta := types.PyFlowKarta()
			accessor, masterComp := accessorForObject(pyflowKarta, pyflowObject, "master")

			podMetadata := []metav1.ObjectMeta{{
				Labels: map[string]string{"test": "value"},
			}}

			err := accessor.UpdatePodMetadata(ctx, masterComp.definition, podMetadata)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(noMetadataError))
		})
	})

	Describe("ApplySuspendActions", func() {
		It("should apply each suspend action in sequence and mutate the object", func() {
			reactorObject := types.NewReactorObject()
			reactorKarta := types.ReactorKarta()
			reactorKarta.Spec.StructureDefinition.RootComponent.SuspendDefinition = &v1alpha1.SuspendDefinition{
				SuspendActions: []v1alpha1.SuspendAction{
					{Path: ".spec.suspend", Value: "true"},
					{Path: ".metadata.labels.state", Value: `"suspended"`},
				},
				ResumeActions: []v1alpha1.SuspendAction{
					{Path: ".spec.suspend", Value: "false"},
				},
			}
			accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

			err := accessor.ApplySuspendActions(ctx, reactorComp.definition)
			Expect(err).ToNot(HaveOccurred())

			obj, err := accessor.GetObject()
			Expect(err).ToNot(HaveOccurred())
			Expect(obj["spec"].(map[string]any)["suspend"]).To(BeTrue())
			Expect(obj["metadata"].(map[string]any)["labels"].(map[string]any)["state"]).To(Equal("suspended"))
		})

		It("should return DefinitionNotFoundError when SuspendDefinition is nil", func() {
			reactorObject := types.NewReactorObject()
			reactorKarta := types.ReactorKarta()
			reactorKarta.Spec.StructureDefinition.RootComponent.SuspendDefinition = nil
			accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

			err := accessor.ApplySuspendActions(ctx, reactorComp.definition)
			Expect(err).To(HaveOccurred())
			var defErr DefinitionNotFoundError
			Expect(errors.As(err, &defErr)).To(BeTrue())
		})
	})

	Describe("ApplyResumeActions", func() {
		It("should apply each resume action in sequence and mutate the object", func() {
			reactorObject := types.NewReactorObject()
			reactorKarta := types.ReactorKarta()
			reactorKarta.Spec.StructureDefinition.RootComponent.SuspendDefinition = &v1alpha1.SuspendDefinition{
				SuspendActions: []v1alpha1.SuspendAction{
					{Path: ".spec.suspend", Value: "true"},
				},
				ResumeActions: []v1alpha1.SuspendAction{
					{Path: ".spec.suspend", Value: "false"},
					{Path: ".metadata.labels.state", Value: `"running"`},
				},
			}
			accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

			Expect(accessor.ApplySuspendActions(ctx, reactorComp.definition)).To(Succeed())
			Expect(accessor.ApplyResumeActions(ctx, reactorComp.definition)).To(Succeed())

			obj, err := accessor.GetObject()
			Expect(err).ToNot(HaveOccurred())
			Expect(obj["spec"].(map[string]any)["suspend"]).To(BeFalse())
			Expect(obj["metadata"].(map[string]any)["labels"].(map[string]any)["state"]).To(Equal("running"))
		})

		It("should return DefinitionNotFoundError when SuspendDefinition is nil", func() {
			reactorObject := types.NewReactorObject()
			reactorKarta := types.ReactorKarta()
			reactorKarta.Spec.StructureDefinition.RootComponent.SuspendDefinition = nil
			accessor, reactorComp := accessorForObject(reactorKarta, reactorObject, "reactor")

			err := accessor.ApplyResumeActions(ctx, reactorComp.definition)
			Expect(err).To(HaveOccurred())
			var defErr DefinitionNotFoundError
			Expect(errors.As(err, &defErr)).To(BeTrue())
		})
	})
})
