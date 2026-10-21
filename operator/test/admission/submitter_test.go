// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package admission

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	batchv1ac "k8s.io/client-go/applyconfigurations/batch/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	capturedNS   = "captured"
	uncapturedNS = "uncaptured"

	// Captures Jobs, excludes CronJobs and skips Jobs named test-*; the
	// immutability policy still covers both types.
	baseValues = `
admissionPolicies:
  submitter:
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

var _ = Describe("Submitter admission policies", Ordered, func() {
	BeforeAll(func() {
		for _, ns := range []*corev1.Namespace{
			{ObjectMeta: metav1.ObjectMeta{Name: capturedNS, Labels: map[string]string{"karta.run.ai/capture-submitter": "true"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: uncapturedNS}},
		} {
			Expect(adminClient.Create(testCtx, ns)).To(Succeed())
		}
	})

	Context("capture without immutability", Ordered, func() {
		BeforeAll(func() {
			install(baseValues + "    enabled: true\n")
			waitForCapture()
		})

		It("records the calling user and service account", func() {
			for _, user := range []string{"alice", serviceAccountUser} {
				job := createJob(user, capturedNS, nil)
				Expect(job.Annotations).To(HaveKeyWithValue(submittedBy, user))
			}
		})

		It("accepts a supplied value naming the caller", func() {
			job := createJob("alice", capturedNS, map[string]string{submittedBy: "alice"})
			Expect(job.Annotations).To(HaveKeyWithValue(submittedBy, "alice"))
		})

		It("rejects a forged identity", func() {
			err := userClients["alice"].Create(testCtx, newJob(capturedNS, map[string]string{submittedBy: "bob"}))
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "got %v", err)
			Expect(err).To(MatchError(ContainSubstring("must be unset or name the requesting user")))
		})

		It("ignores namespaces outside the selector", func() {
			Expect(createJob("alice", uncapturedNS, nil).Annotations).NotTo(HaveKey(submittedBy))
			job := createJob("alice", uncapturedNS, map[string]string{submittedBy: "bob"})
			Expect(job.Annotations).To(HaveKeyWithValue(submittedBy, "bob"))
		})

		It("ignores excluded resources", func() {
			cronJob := newCronJob(capturedNS, map[string]string{submittedBy: "bob"})
			Expect(userClients["alice"].Create(testCtx, cronJob)).To(Succeed())
			Expect(cronJob.Annotations).To(HaveKeyWithValue(submittedBy, "bob"))
		})

		It("ignores requests failing matchConditions", func() {
			job := newJob(capturedNS, nil)
			job.Name = "test-" + job.Name
			Expect(userClients["alice"].Create(testCtx, job)).To(Succeed())
			Expect(job.Annotations).NotTo(HaveKey(submittedBy))
		})

		It("ignores objects created by a controller", func() {
			job := newJob(capturedNS, map[string]string{submittedBy: "alice"})
			job.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: "batch/v1", Kind: "CronJob", Name: "nightly", UID: "0000-0000", Controller: ptr.To(true),
			}}
			Expect(userClients[serviceAccountUser].Create(testCtx, job)).To(Succeed())
			Expect(job.Annotations).To(HaveKeyWithValue(submittedBy, "alice"))
		})

		It("leaves the annotation editable", func() {
			job := createJob("alice", capturedNS, nil)
			job.Annotations[submittedBy] = "bob"
			Expect(userClients["alice"].Update(testCtx, job)).To(Succeed())
		})
	})

	Context("capture with immutability", Ordered, func() {
		var job *batchv1.Job

		BeforeAll(func() {
			install(baseValues + "    enabled: true\n    immutable: true\n")
			waitForCapture()
			waitForImmutable()
			job = createJob("alice", capturedNS, nil)
		})

		It("rejects changing or removing the annotation", func() {
			expectImmutable(job)
		})

		It("rejects adding the annotation on update", func() {
			unannotated := createJob("alice", uncapturedNS, nil)
			unannotated.Annotations = map[string]string{submittedBy: "alice"}
			err := userClients["alice"].Update(testCtx, unannotated)
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "got %v", err)
		})

		It("allows other updates", func() {
			Expect(userClients["alice"].Get(testCtx, client.ObjectKeyFromObject(job), job)).To(Succeed())
			job.Labels = map[string]string{"team": "a"}
			Expect(userClients["alice"].Update(testCtx, job)).To(Succeed())
		})

		// The Job status strategy keeps metadata changes, so /status must be covered.
		It("rejects changes through the status subresource and allows other status updates", func() {
			current := &batchv1.Job{}
			Expect(userClients["bob"].Get(testCtx, client.ObjectKeyFromObject(job), current)).To(Succeed())
			Expect(userClients["bob"].Status().Update(testCtx, current)).To(Succeed())
			current.Annotations[submittedBy] = "bob"
			err := userClients["bob"].Status().Update(testCtx, current)
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "got %v", err)
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

		It("protects annotations outside the capture scope", func() {
			cronJob := newCronJob(capturedNS, map[string]string{submittedBy: "bob"})
			Expect(userClients["alice"].Create(testCtx, cronJob)).To(Succeed())
			cronJob.Annotations[submittedBy] = "alice"
			err := userClients["alice"].Update(testCtx, cronJob)
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "got %v", err)
		})

		It("stays in force after the namespace leaves the selector", func() {
			ns := &corev1.Namespace{}
			Expect(adminClient.Get(testCtx, client.ObjectKey{Name: capturedNS}, ns)).To(Succeed())
			delete(ns.Labels, "karta.run.ai/capture-submitter")
			Expect(adminClient.Update(testCtx, ns)).To(Succeed())
			DeferCleanup(func() {
				Expect(adminClient.Get(testCtx, client.ObjectKey{Name: capturedNS}, ns)).To(Succeed())
				ns.Labels["karta.run.ai/capture-submitter"] = "true"
				Expect(adminClient.Update(testCtx, ns)).To(Succeed())
			})

			Eventually(func(g Gomega) {
				g.Expect(createJob("alice", capturedNS, nil).Annotations).NotTo(HaveKey(submittedBy))
			}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
			expectImmutable(job)
		})

		It("stays in force after capture is disabled", func() {
			install(baseValues + "    enabled: false\n    immutable: true\n")
			Eventually(func(g Gomega) {
				g.Expect(createJob("alice", capturedNS, nil).Annotations).NotTo(HaveKey(submittedBy))
			}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
			expectImmutable(job)
		})

		It("lifts protection when immutability is disabled", func() {
			install(baseValues)
			Eventually(func(g Gomega) {
				current := &batchv1.Job{}
				g.Expect(userClients["alice"].Get(testCtx, client.ObjectKeyFromObject(job), current)).To(Succeed())
				current.Annotations[submittedBy] = "bob"
				g.Expect(userClients["alice"].Update(testCtx, current)).To(Succeed())
			}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
		})
	})

	It("rejects requests when a policy expression fails", func() {
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
      - name: missing-label
        expression: 'object.metadata.labels["missing"] == "x"'
`)
		Eventually(func(g Gomega) {
			g.Expect(userClients["alice"].Create(testCtx, newJob(capturedNS, nil))).NotTo(Succeed())
		}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
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
admissionPolicies: {submitter: {immutable: true, apiVersion: v1}}`,
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
	Entry("subresource", `
admissionPolicies: {submitter: {enabled: true, apiVersion: v1, resources: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs/status]}]}}`,
		`subresource "jobs/status" is not supported`),
)

var _ = DescribeTable("Rendering detects the MutatingAdmissionPolicy version",
	func(served, want string) {
		objs, err := render(`
admissionPolicies: {submitter: {enabled: true, resources: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs]}]}}`,
			"--api-versions", "admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy", "--api-versions", served)
		Expect(err).NotTo(HaveOccurred())
		var versions []string
		for _, obj := range objs {
			if obj.GetKind() == "MutatingAdmissionPolicy" || obj.GetKind() == "MutatingAdmissionPolicyBinding" {
				versions = append(versions, obj.GetAPIVersion())
			}
		}
		Expect(versions).To(ConsistOf(want, want))
	},
	Entry("v1", "admissionregistration.k8s.io/v1/MutatingAdmissionPolicy", "admissionregistration.k8s.io/v1"),
	Entry("v1beta1", "admissionregistration.k8s.io/v1beta1/MutatingAdmissionPolicy", "admissionregistration.k8s.io/v1beta1"),
)

// waitForCapture blocks until the capture policies are loaded.
func waitForCapture() {
	Eventually(func(g Gomega) {
		g.Expect(createJob("alice", capturedNS, nil).Annotations).To(HaveKeyWithValue(submittedBy, "alice"))
	}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
}

// waitForImmutable blocks until the immutability policy is loaded. Each attempt
// probes a new Job, since an update accepted before then removes the annotation.
func waitForImmutable() {
	Eventually(func(g Gomega) {
		probe := createJob("alice", capturedNS, nil)
		delete(probe.Annotations, submittedBy)
		g.Expect(apierrors.IsForbidden(userClients["alice"].Update(testCtx, probe))).To(BeTrue())
	}, eventuallyTimeout, eventuallyInterval).Should(Succeed())
}

func expectImmutable(job *batchv1.Job) {
	for _, change := range []func(map[string]string){
		func(a map[string]string) { a[submittedBy] = "bob" },
		func(a map[string]string) { delete(a, submittedBy) },
	} {
		current := &batchv1.Job{}
		Expect(adminClient.Get(testCtx, client.ObjectKeyFromObject(job), current)).To(Succeed())
		change(current.Annotations)
		err := userClients["alice"].Update(testCtx, current)
		Expect(apierrors.IsForbidden(err)).To(BeTrue(), "got %v", err)
		Expect(err).To(MatchError(ContainSubstring("karta.run.ai/submitted-by is immutable and can only be set on create")))
	}
}

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
