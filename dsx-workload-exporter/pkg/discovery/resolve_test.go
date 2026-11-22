// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package discovery

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type reader struct {
	objects     map[string]*metav1.PartialObjectMetadata
	cached      map[string]bool
	err         error
	reads       []string
	nilCached   bool
	nilMetadata bool
}

func (r *reader) Cached(ref ObjectRef) (*unstructured.Unstructured, bool) {
	if !r.cached[ref.Name] {
		return nil, false
	}
	if r.nilCached {
		return nil, true
	}
	m := r.objects[ref.Name]
	u := &unstructured.Unstructured{}
	u.SetName(m.Name)
	u.SetNamespace(m.Namespace)
	u.SetUID(m.UID)
	u.SetResourceVersion(m.ResourceVersion)
	u.SetOwnerReferences(m.OwnerReferences)
	return u, true
}
func (r *reader) GetMetadata(_ context.Context, ref ObjectRef) (*metav1.PartialObjectMetadata, error) {
	r.reads = append(r.reads, ref.Name)
	if r.err != nil {
		return nil, r.err
	}
	if r.nilMetadata {
		return nil, nil
	}
	if m := r.objects[ref.Name]; m != nil {
		return m.DeepCopy(), nil
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "objects"}, ref.Name)
}

func TestDiscovery(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Discovery") }

var _ = Describe("root resolution", func() {
	var r *reader
	var d Discoverer
	var ref ObjectRef
	pod := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	job := schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
	root := schema.GroupVersionKind{Group: "example.org", Version: "v1", Kind: "JobSet"}
	BeforeEach(func() {
		yes := true
		r = &reader{objects: map[string]*metav1.PartialObjectMetadata{}, cached: map[string]bool{"pod": true}}
		for _, name := range []string{"pod", "job", "root"} {
			r.objects[name] = &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(name), ResourceVersion: "1"}}
		}
		r.objects["pod"].OwnerReferences = []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: "job", UID: "job", Controller: &yes}}
		r.objects["job"].OwnerReferences = []metav1.OwnerReference{{APIVersion: "example.org/v1", Kind: "JobSet", Name: "root", UID: "root", Controller: &yes}}
		var err error
		d, err = New(r, Config{})
		Expect(err).NotTo(HaveOccurred())
		ref = ObjectRef{GVK: pod, Namespace: "ns", Name: "pod", UID: "pod"}
	})
	It("returns the complete ownership path and observed versions", func() {
		r.objects["job"].ResourceVersion = "2"
		r.objects["root"].ResourceVersion = "3"
		result, err := d.Get(context.Background(), ref)
		Expect(err).NotTo(HaveOccurred())
		path := []ObjectRef{
			{GVK: pod, Namespace: "ns", Name: "pod", UID: "pod", ResourceVersion: "1"},
			{GVK: job, Namespace: "ns", Name: "job", UID: "job", ResourceVersion: "2"},
			{GVK: root, Namespace: "ns", Name: "root", UID: "root", ResourceVersion: "3"},
		}
		Expect(result.Workload).To(Equal(&GenericWorkload{Root: path[2], Path: path, Resolution: Resolved}))
		Expect(r.reads).To(Equal([]string{"job", "root"}))
	})
	It("resolves a standalone object and fills its identity", func() {
		r.objects["pod"].OwnerReferences = nil
		ref.UID = ""
		result, err := d.Get(context.Background(), ref)
		Expect(err).NotTo(HaveOccurred())
		want := ObjectRef{GVK: pod, Namespace: "ns", Name: "pod", UID: "pod", ResourceVersion: "1"}
		Expect(result.Workload).To(Equal(&GenericWorkload{Root: want, Path: []ObjectRef{want}, Resolution: Resolved}))
		Expect(r.reads).To(BeEmpty())
	})
	It("stops only at an explicit ignored owner", func() {
		d, _ = New(r, Config{IgnoredTopOwners: []schema.GroupVersionKind{root}})
		result, err := d.Get(context.Background(), ref)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Workload.Root.GVK).To(Equal(job))
		Expect(result.Workload.Resolution).To(Equal(IgnoredOwner))
		Expect(r.reads).To(Equal([]string{"job"}))
	})
	DescribeTable("requires an exact ignored owner GVK", func(ignored schema.GroupVersionKind) {
		var err error
		d, err = New(r, Config{IgnoredTopOwners: []schema.GroupVersionKind{ignored}})
		Expect(err).NotTo(HaveOccurred())
		result, err := d.Get(context.Background(), ref)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Workload.Root.GVK).To(Equal(root))
		Expect(result.Workload.Resolution).To(Equal(Resolved))
		Expect(r.reads).To(Equal([]string{"job", "root"}))
	},
		Entry("different group", schema.GroupVersionKind{Group: "other.org", Version: "v1", Kind: "JobSet"}),
		Entry("different version", schema.GroupVersionKind{Group: "example.org", Version: "v2", Kind: "JobSet"}),
		Entry("different kind", schema.GroupVersionKind{Group: "example.org", Version: "v1", Kind: "Other"}),
	)
	It("does not fabricate a root for missing owners", func() {
		delete(r.objects, "root")
		result, err := d.Get(context.Background(), ref)
		Expect(errors.Is(err, ErrUnresolvedOwner)).To(BeTrue())
		Expect(result.Workload).To(BeNil())
	})
	It("rejects replacement UIDs", func() {
		r.objects["job"].UID = "replacement"
		_, err := d.Get(context.Background(), ref)
		Expect(errors.Is(err, ErrStale)).To(BeTrue())
	})
	It("rejects cycles", func() {
		r.objects["root"].OwnerReferences = r.objects["pod"].OwnerReferences
		_, err := d.Get(context.Background(), ref)
		Expect(errors.Is(err, ErrUnresolvedOwner)).To(BeTrue())
	})
	It("detects a repeated UID across served API versions", func() {
		yes := true
		r.objects["pod"].OwnerReferences = []metav1.OwnerReference{{APIVersion: "v2", Kind: "Pod", Name: "pod", UID: "pod", Controller: &yes}}
		result, err := d.Get(context.Background(), ref)
		Expect(errors.Is(err, ErrUnresolvedOwner)).To(BeTrue())
		Expect(result.Workload).To(BeNil())
	})
	DescribeTable("resolves chains within the depth bound", func(depth int) {
		yes := true
		for n := 0; n < depth; n++ {
			name := fmt.Sprintf("owner-%d", n)
			r.objects[name] = &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(name), ResourceVersion: "1"}}
			if n < depth-1 {
				next := fmt.Sprintf("owner-%d", n+1)
				r.objects[name].OwnerReferences = []metav1.OwnerReference{{APIVersion: "example.org/v1", Kind: "Owner", Name: next, UID: types.UID(next), Controller: &yes}}
			}
		}
		r.objects["pod"].OwnerReferences = []metav1.OwnerReference{{APIVersion: "example.org/v1", Kind: "Owner", Name: "owner-0", UID: "owner-0", Controller: &yes}}
		result, err := d.Get(context.Background(), ref)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Workload.Path).To(HaveLen(depth + 1))
		Expect(result.Workload.Root.UID).To(Equal(types.UID(fmt.Sprintf("owner-%d", depth-1))))
	}, Entry("beyond initial path capacity", 8), Entry("exactly 1024 objects", 1023))

	It("rejects an overlong chain before reading beyond the bound", func() {
		yes := true
		for n := 0; n < 1024; n++ {
			name := fmt.Sprintf("owner-%d", n)
			next := fmt.Sprintf("owner-%d", n+1)
			r.objects[name] = &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(name), ResourceVersion: "1", OwnerReferences: []metav1.OwnerReference{{APIVersion: "example.org/v1", Kind: "Owner", Name: next, UID: types.UID(next), Controller: &yes}}}}
		}
		r.objects["pod"].OwnerReferences = []metav1.OwnerReference{{APIVersion: "example.org/v1", Kind: "Owner", Name: "owner-0", UID: "owner-0", Controller: &yes}}
		result, err := d.Get(context.Background(), ref)
		Expect(errors.Is(err, ErrUnresolvedOwner)).To(BeTrue())
		Expect(result.Workload).To(BeNil())
		Expect(r.reads).NotTo(ContainElement("owner-1023"))
	})

	It("requires an object reader", func() {
		_, err := New(nil, Config{})
		Expect(errors.Is(err, ErrReaderRequired)).To(BeTrue())
	})
	It("honors cancellation before reading", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := d.Get(ctx, ref)
		Expect(err).To(MatchError(context.Canceled))
		Expect(r.reads).To(BeEmpty())
	})
	DescribeTable("rejects malformed controller references", func(field string) {
		owner := &r.objects["pod"].OwnerReferences[0]
		switch field {
		case "apiVersion":
			owner.APIVersion = ""
		case "version":
			owner.APIVersion = "batch/"
		case "malformed":
			owner.APIVersion = "batch/v1/extra"
		case "kind":
			owner.Kind = ""
		case "name":
			owner.Name = ""
		case "uid":
			owner.UID = ""
		case "multiple":
			r.objects["pod"].OwnerReferences = append(r.objects["pod"].OwnerReferences, *owner)
		}
		result, err := d.Get(context.Background(), ref)
		Expect(errors.Is(err, ErrUnresolvedOwner)).To(BeTrue())
		Expect(result.Workload).To(BeNil())
		Expect(r.reads).To(BeEmpty())
	}, Entry("empty apiVersion", "apiVersion"), Entry("empty version", "version"), Entry("malformed apiVersion", "malformed"), Entry("empty kind", "kind"), Entry("empty name", "name"), Entry("empty UID", "uid"), Entry("multiple controllers", "multiple"))
	It("ignores non-controller references", func() {
		no := false
		r.objects["pod"].OwnerReferences = append(r.objects["pod"].OwnerReferences, metav1.OwnerReference{}, metav1.OwnerReference{Controller: &no})
		result, err := d.Get(context.Background(), ref)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Workload.Root.GVK).To(Equal(root))
	})
	DescribeTable("rejects changed input identity", func(field string) {
		switch field {
		case "name":
			r.objects["pod"].Name = "replacement"
		case "namespace":
			r.objects["pod"].Namespace = "other"
		case "uid":
			r.objects["pod"].UID = ""
		case "version":
			ref.ResourceVersion = "old"
		}
		_, err := d.Get(context.Background(), ref)
		Expect(errors.Is(err, ErrStale)).To(BeTrue())
	}, Entry("name", "name"), Entry("namespace", "namespace"), Entry("empty UID", "uid"), Entry("resource version", "version"))
	DescribeTable("classifies reader errors and preserves their cause", func(cause, want error) {
		r.err = cause
		result, err := d.Get(context.Background(), ref)
		Expect(errors.Is(err, want)).To(BeTrue())
		Expect(errors.Is(err, cause)).To(BeTrue())
		Expect(result.Workload).To(BeNil())
	},
		Entry("pending", fmt.Errorf("cache: %w", ErrPending), ErrPending),
		Entry("stale", fmt.Errorf("cache: %w", ErrStale), ErrStale),
		Entry("forbidden", apierrors.NewForbidden(schema.GroupResource{}, "job", errors.New("denied")), ErrPermissionDenied),
		Entry("unauthorized", apierrors.NewUnauthorized("denied"), ErrPermissionDenied),
		Entry("permission denied", fmt.Errorf("reader: %w", ErrPermissionDenied), ErrPermissionDenied),
		Entry("missing owner", apierrors.NewNotFound(schema.GroupResource{}, "job"), ErrUnresolvedOwner),
		Entry("unresolved owner", fmt.Errorf("reader: %w", ErrUnresolvedOwner), ErrUnresolvedOwner),
		Entry("transient", errors.New("transport"), ErrUnavailable),
	)
	It("rejects an empty metadata response", func() {
		r.nilMetadata = true
		result, err := d.Get(context.Background(), ref)
		Expect(errors.Is(err, ErrUnavailable)).To(BeTrue())
		Expect(result.Workload).To(BeNil())
		Expect(r.reads).To(Equal([]string{"job"}))
	})
	It("fetches metadata when a cache hit contains no object", func() {
		r.nilCached = true
		result, err := d.Get(context.Background(), ref)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Workload.Root.GVK).To(Equal(root))
		Expect(r.reads).To(Equal([]string{"pod", "job", "root"}))
	})
	It("normalizes cluster-scoped owner namespaces", func() {
		r.objects["root"].Namespace = ""
		result, err := d.Get(context.Background(), ref)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Workload.Root.Namespace).To(BeEmpty())
	})
	It("uses a cached root without a metadata read", func() {
		r.cached["root"] = true
		result, err := d.Get(context.Background(), ref)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Workload.Root.GVK).To(Equal(root))
		Expect(r.reads).To(Equal([]string{"job"}))
	})
})
