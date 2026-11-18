// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package admission

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	batchv1ac "k8s.io/client-go/applyconfigurations/batch/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	capturedNS   = "captured"
	uncapturedNS = "uncaptured"

	// Captures Jobs, excludes CronJobs and skips Jobs named test-*.
	// Kubernetes limit on the total bytes of all annotation keys and values.
	annotationsMaxBytes = 256 * 1024

	baseValues = `
admissionPolicies:
  submitter:
    enabled: true
    apiVersion: v1
    resources:
      - apiGroups: ["batch"]
        apiVersions: ["v1"]
        resources: ["jobs", "cronjobs"]
    excludeResources:
      - apiGroups: ["batch"]
        apiVersions: ["v1"]
        resources: ["cronjobs"]
    matchConditions:
      - name: skip-test-jobs
        expression: '!object.metadata.name.startsWith("test-")'
`
)

var jobCount int

var _ = Describe("Submitter admission policy", Ordered, func() {
	BeforeAll(func() {
		for _, ns := range []*corev1.Namespace{
			{ObjectMeta: metav1.ObjectMeta{Name: capturedNS, Labels: map[string]string{"karta.run.ai/capture-submitter": "true"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: uncapturedNS}},
		} {
			Expect(adminClient.Create(testCtx, ns)).To(Succeed())
		}
	})

	Context("capture", Ordered, func() {
		BeforeAll(func() {
			install(baseValues)
			Eventually(func(g Gomega) {
				g.Expect(createJob("alice", capturedNS, nil).Annotations).To(HaveKeyWithValue(submittedBy, "alice"))
			}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
		})

		It("records the calling user and service account", func() {
			for _, user := range []string{"alice", serviceAccountUser} {
				job := createJob(user, capturedNS, nil)
				Expect(job.Annotations).To(HaveKeyWithValue(submittedBy, user))
			}
		})

		It("keeps a supplied value", func() {
			for _, value := range []string{"alice", "bob"} {
				job := createJob("alice", capturedNS, map[string]string{submittedBy: value})
				Expect(job.Annotations).To(HaveKeyWithValue(submittedBy, value))
			}
		})

		It("ignores namespaces outside the selector", func() {
			Expect(createJob("alice", uncapturedNS, nil).Annotations).NotTo(HaveKey(submittedBy))
		})

		It("ignores excluded resources", func() {
			cronJob := newCronJob(capturedNS, nil)
			Expect(userClients["alice"].Create(testCtx, cronJob)).To(Succeed())
			Expect(cronJob.Annotations).NotTo(HaveKey(submittedBy))
		})

		It("ignores requests failing matchConditions", func() {
			job := newJob(capturedNS, nil)
			job.Name = "test-" + job.Name
			Expect(userClients["alice"].Create(testCtx, job)).To(Succeed())
			Expect(job.Annotations).NotTo(HaveKey(submittedBy))
		})

		It("ignores objects created by a controller", func() {
			job := newJob(capturedNS, nil)
			job.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: "batch/v1", Kind: "CronJob", Name: "nightly", UID: "0000-0000", Controller: ptr.To(true),
			}}
			Expect(userClients[serviceAccountUser].Create(testCtx, job)).To(Succeed())
			Expect(job.Annotations).NotTo(HaveKey(submittedBy))
		})

		It("leaves the annotation editable", func() {
			job := createJob("alice", capturedNS, nil)
			job.Annotations[submittedBy] = "bob"
			Expect(userClients["alice"].Update(testCtx, job)).To(Succeed())
		})

		// The mutation is not attributed to the applying field manager, so an apply
		// that omits the annotation does not remove it.
		It("keeps the annotation across server-side applies that omit it", func() {
			jobCount++
			name := fmt.Sprintf("applied-%d", jobCount)
			apply := func(labels map[string]string) error {
				job := batchv1ac.Job(name, capturedNS).WithLabels(labels).WithSpec(batchv1ac.JobSpec().
					WithTemplate(corev1ac.PodTemplateSpec().WithSpec(corev1ac.PodSpec().
						WithRestartPolicy(corev1.RestartPolicyNever).
						WithContainers(corev1ac.Container().WithName("main").WithImage("registry.example.com/main:v1")))))
				return userClients["alice"].Apply(testCtx, job, client.FieldOwner("gitops"))
			}
			Expect(apply(map[string]string{"rev": "1"})).To(Succeed())
			Expect(apply(map[string]string{"rev": "2"})).To(Succeed())

			current := &batchv1.Job{}
			Expect(adminClient.Get(testCtx, client.ObjectKey{Namespace: capturedNS, Name: name}, current)).To(Succeed())
			Expect(current.Labels).To(HaveKeyWithValue("rev", "2"))
			Expect(current.Annotations).To(HaveKeyWithValue(submittedBy, "alice"))
		})

		// The limit counts bytes across all annotation keys and values.
		It("captures when the annotation exactly fits the size limit", func() {
			filler := strings.Repeat("x", annotationsMaxBytes-len("big")-len(submittedBy)-len("alice"))
			job := createJob("alice", capturedNS, map[string]string{"big": filler})
			Expect(job.Annotations).To(HaveKeyWithValue(submittedBy, "alice"))
		})

		// Two-byte characters: the length in characters fits, the length in bytes
		// does not, so the size must be counted in bytes.
		It("skips capture when the annotation would exceed the size limit", func() {
			filler := strings.Repeat("\u00e9", (annotationsMaxBytes+1-len("big")-len(submittedBy)-len("alice"))/2)
			job := createJob("alice", capturedNS, map[string]string{"big": filler})
			Expect(job.Annotations).NotTo(HaveKey(submittedBy))
		})
	})

	It("records the caller in a custom annotation", func() {
		install(baseValues + "    annotation: example.com/owner\n")
		Eventually(func(g Gomega) {
			g.Expect(createJob("alice", capturedNS, nil).Annotations).To(HaveKeyWithValue("example.com/owner", "alice"))
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())

		job := createJob("alice", capturedNS, nil)
		Expect(job.Annotations).NotTo(HaveKey(submittedBy))
		supplied := createJob("alice", capturedNS, map[string]string{"example.com/owner": "bob"})
		Expect(supplied.Annotations).To(HaveKeyWithValue("example.com/owner", "bob"))
	})

	It("admits requests unchanged when a policy expression fails", func() {
		// The condition errors on Jobs without the probe label.
		install(`
admissionPolicies:
  submitter:
    enabled: true
    apiVersion: v1
    resources:
      - apiGroups: ["batch"]
        apiVersions: ["v1"]
        resources: ["jobs"]
    matchConditions:
      - name: probe-label
        expression: 'object.metadata.labels["probe"] == "on"'
`)
		// The previous policy annotated every Job, so an admitted Job with no
		// annotations shows the new policy is loaded.
		Eventually(func(g Gomega) {
			g.Expect(createJob("alice", capturedNS, nil).Annotations).To(BeEmpty())
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())

		labeled := newJob(capturedNS, nil)
		labeled.Labels = map[string]string{"probe": "on"}
		Expect(userClients["alice"].Create(testCtx, labeled)).To(Succeed())
		Expect(labeled.Annotations).To(HaveKeyWithValue(submittedBy, "alice"))
	})
})

var _ = DescribeTable("Rendering rejects invalid values",
	func(values, wantErr string, args ...string) {
		_, err := render(values, args...)
		Expect(err).To(MatchError(ContainSubstring(wantErr)))
	},
	Entry("no cluster APIs and no apiVersion", `
admissionPolicies: {submitter: {enabled: true, resources: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs]}]}}`,
		"set admissionPolicies.submitter.apiVersion when rendering without a cluster"),
	Entry("MAP API missing", `
admissionPolicies: {submitter: {enabled: true, resources: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs]}]}}`,
		"requires MutatingAdmissionPolicy", "--api-versions", "admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy"),
	Entry("unknown apiVersion", `
admissionPolicies: {submitter: {enabled: true, apiVersion: v2, resources: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs]}]}}`,
		"must be v1 or v1beta1"),
	Entry("no resources", `
admissionPolicies: {submitter: {enabled: true, apiVersion: v1}}`,
		"admissionPolicies.submitter.resources must list at least one resource"),
	Entry("operations set by the user", `
admissionPolicies: {submitter: {enabled: true, apiVersion: v1, resources: [{operations: [UPDATE], apiGroups: [batch], apiVersions: [v1], resources: [jobs]}]}}`,
		`unsupported key "operations"`),
	Entry("empty rule field", `
admissionPolicies: {submitter: {enabled: true, apiVersion: v1, resources: [{apiGroups: [batch], apiVersions: [], resources: [jobs]}]}}`,
		"resources[0].apiVersions must be a nonempty list"),
	Entry("reserved matchConditions name", `
admissionPolicies: {submitter: {enabled: true, apiVersion: v1, resources: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs]}], matchConditions: [{name: karta-x, expression: "true"}]}}`,
		`name "karta-x" uses the reserved karta- prefix`),
	Entry("empty annotation", `
admissionPolicies: {submitter: {enabled: true, apiVersion: v1, annotation: "", resources: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs]}]}}`,
		"admissionPolicies.submitter.annotation must be set"),
	Entry("invalid annotation key", `
admissionPolicies: {submitter: {enabled: true, apiVersion: v1, annotation: "Example.com/owner", resources: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs]}]}}`,
		`"Example.com/owner" is not a valid annotation key`),
	Entry("annotation key with a quote", `
admissionPolicies: {submitter: {enabled: true, apiVersion: v1, annotation: "owner\"", resources: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs]}]}}`,
		"is not a valid annotation key"),
	Entry("subresource", `
admissionPolicies: {submitter: {enabled: true, apiVersion: v1, resources: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs/status]}]}}`,
		`subresource "jobs/status" is not supported`),
)

// helm upgrade --reuse-values keeps the values of a release whose chart had no
// admissionPolicies, so the new defaults are missing.
var _ = DescribeTable("Rendering without submitter values",
	func(values string) {
		objs, err := renderPolicies(values)
		Expect(err).NotTo(HaveOccurred())
		Expect(objs).To(BeEmpty())
	},
	Entry("no admissionPolicies", "admissionPolicies: null\n"),
	Entry("no submitter", "admissionPolicies: {submitter: null}\n"),
)

var _ = DescribeTable("Rendering detects the MutatingAdmissionPolicy version",
	func(served, want string) {
		objs, err := renderPolicies(`
admissionPolicies: {submitter: {enabled: true, resources: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs]}]}}`,
			"--api-versions", served)
		Expect(err).NotTo(HaveOccurred())
		var versions []string
		for _, obj := range objs {
			versions = append(versions, obj.GetAPIVersion())
		}
		Expect(versions).To(ConsistOf(want, want))
	},
	Entry("v1", "admissionregistration.k8s.io/v1/MutatingAdmissionPolicy", "admissionregistration.k8s.io/v1"),
	Entry("v1beta1", "admissionregistration.k8s.io/v1beta1/MutatingAdmissionPolicy", "admissionregistration.k8s.io/v1beta1"),
)

func createJob(user, namespace string, annotations map[string]string) *batchv1.Job {
	job := newJob(namespace, annotations)
	ExpectWithOffset(1, userClients[user].Create(testCtx, job)).To(Succeed())
	return job
}

func newJob(namespace string, annotations map[string]string) *batchv1.Job {
	jobCount++
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("job-%d", jobCount), Namespace: namespace, Annotations: annotations},
		Spec:       batchv1.JobSpec{Template: podTemplate()},
	}
}

func newCronJob(namespace string, annotations map[string]string) *batchv1.CronJob {
	jobCount++
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("cronjob-%d", jobCount), Namespace: namespace, Annotations: annotations},
		Spec: batchv1.CronJobSpec{
			Schedule:    "@daily",
			JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: podTemplate()}},
		},
	}
}

func podTemplate() corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{Spec: corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers:    []corev1.Container{{Name: "main", Image: "registry.example.com/main:v1"}},
	}}
}
