// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// This tool lives in its own module so its registry client (go-containerregistry)
// never enters Karta's shipped dependency graph or its generated license list.
module github.com/dsx-ai-factory/workload-map/hack/imagelock

go 1.26.8

require (
	github.com/google/go-containerregistry v0.22.1
	sigs.k8s.io/yaml v1.6.0
)

require (
	github.com/docker/cli v29.7.2+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.9.3 // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	gotest.tools/v3 v3.5.2 // indirect
)
