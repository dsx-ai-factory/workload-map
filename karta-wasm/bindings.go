// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"syscall/js"

	"github.com/dsx-ai-factory/workload-map/karta-wasm/core"
)

func jsBuildTree(_ js.Value, arguments []js.Value) any {
	if len(arguments) != 2 {
		return encodeEnvelope(nil, fmt.Errorf("buildTree: expected 2 arguments, got %d", len(arguments)))
	}
	definitionJSON := arguments[0].String()
	workloadJSON := arguments[1].String()
	workloadTree, err := core.BuildTree(context.Background(), definitionJSON, workloadJSON)
	return encodeEnvelope(workloadTree, err)
}

func jsListCatalog(js.Value, []js.Value) any {
	return encodeEnvelope(core.ListCatalog(), nil)
}
