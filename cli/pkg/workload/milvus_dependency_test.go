// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package workload

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Milvus dependency pod attribution", func() {
	It("places scoped etcd and MinIO pods under their components", func() {
		view := describeObject([]byte(`
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: vectors
  namespace: ml-team
spec:
  components:
    proxy:
      replicas: 1
`),
			livePod("vectors-etcd-0", "node-01", map[string]string{"app.kubernetes.io/name": "etcd"}, "0"),
			livePod("vectors-minio-0", "node-01", map[string]string{"app": "minio"}, "0"),
			milvusPod("vectors-proxy-0", "proxy"),
		)

		Expect(componentNamed(view.Components, "etcd").Pods).To(ConsistOf(HaveField("Name", "vectors-etcd-0")))
		Expect(componentNamed(view.Components, "minio").Pods).To(ConsistOf(HaveField("Name", "vectors-minio-0")))
		Expect(componentNamed(view.Components, "proxy").Pods).To(ConsistOf(HaveField("Name", "vectors-proxy-0")))
	})
})
