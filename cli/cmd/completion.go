// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/dsx-ai-factory/workload-map/pkg/catalog"
)

// completionTimeout bounds each request a TAB press makes: an unreachable cluster
// should offer nothing rather than freeze the shell. A variable so tests can
// shorten it.
var completionTimeout = 2 * time.Second

// boundRequests caps every request to the cluster at completionTimeout unless
// the user set --request-timeout. Discovery takes no context, so the request
// timeout is the only bound on it. It must run before any client is built.
func boundRequests(cmd *cobra.Command) {
	if !cmd.Flags().Changed("request-timeout") {
		*kubeFlags.Timeout = completionTimeout.String()
	}
}

// completeWorkloads completes the TYPE, TYPE/NAME and TYPE NAME arguments get
// and describe accept. A completion has no channel for an error, so a failure
// to reach the cluster offers nothing rather than printing.
func completeWorkloads(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// Every lookup below reads cmd.Context(), and the __complete process ends
	// after this one call, so replacing the context is safe.
	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()
	cmd.SetContext(ctx)
	boundRequests(cmd)

	typeToken, namePrefix, qualified := strings.Cut(toComplete, "/")

	var candidates []string
	switch {
	case len(args) == 0 && !qualified:
		candidates = completeTypes(cmd, toComplete)
	case len(args) == 0:
		for _, name := range completeNames(cmd, typeToken, namePrefix) {
			candidates = append(candidates, typeToken+"/"+name)
		}
	case len(args) == 1 && !strings.Contains(args[0], "/"):
		candidates = completeNames(cmd, args[0], toComplete)
	}
	return candidates, cobra.ShellCompDirectiveNoFileComp
}

// completeNamespaces completes the -n/--namespace flag with the namespaces of
// the cluster, matching prefix.
func completeNamespaces(cmd *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()
	boundRequests(cmd)

	dyn, err := newDynamicClient(clusterAccess())
	if err != nil {
		cobra.CompDebugln(err.Error(), true)
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	list, err := dyn.Resource(corev1.SchemeGroupVersion.WithResource("namespaces")).List(ctx, metav1.ListOptions{})
	if err != nil {
		cobra.CompDebugln(err.Error(), true)
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var namespaces []string
	for _, item := range list.Items {
		if name := item.GetName(); strings.HasPrefix(name, prefix) {
			namespaces = append(namespaces, name)
		}
	}
	slices.Sort(namespaces)
	return namespaces, cobra.ShellCompDirectiveNoFileComp
}

// completeTypes offers the lowercased root kind of every definition, the form
// kubectl users type, matching prefix. A kind covered at several group versions
// would be rejected as ambiguous, so it is offered in each qualified form instead.
func completeTypes(cmd *cobra.Command, prefix string) []string {
	resolver, _ := loadDefinitions(cmd.Context(), clusterAccess())

	byKind := map[string][]schema.GroupVersionKind{}
	for _, def := range resolver.List() {
		gvk := catalog.RootKey(def.Karta)
		if kind := strings.ToLower(gvk.Kind); kind != "" && !slices.Contains(byKind[kind], gvk) {
			byKind[kind] = append(byKind[kind], gvk)
		}
	}

	var types []string
	for kind, gvks := range byKind {
		var tokens []string
		switch len(gvks) {
		case 1:
			tokens = []string{kind}
		default:
			for _, gvk := range gvks {
				tokens = append(tokens, qualifiedToken(gvk))
			}
		}
		for _, token := range tokens {
			if strings.HasPrefix(token, strings.ToLower(prefix)) {
				types = append(types, token)
			}
		}
	}
	slices.Sort(types)
	return types
}

// completeNames offers the names of the workloads of typeToken in the namespace
// get would search, matching prefix.
func completeNames(cmd *cobra.Command, typeToken, prefix string) []string {
	look, _, err := resolveLookup(cmd, &getOptions{typeToken: typeToken})
	if err != nil {
		cobra.CompDebugln(err.Error(), true)
		return nil
	}

	gvk := catalog.RootKey(look.definition.Karta)
	mapping, err := look.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		cobra.CompDebugln(err.Error(), true)
		return nil
	}
	namespace := look.namespace
	if mapping.Scope.Name() == meta.RESTScopeNameRoot {
		namespace = ""
	}

	objects, err := list(cmd.Context(), look.dyn, mapping, namespace, &getOptions{chunkSize: defaultChunkSize})
	if err != nil {
		cobra.CompDebugln(err.Error(), true)
		return nil
	}

	var names []string
	for _, object := range objects {
		if name := object.GetName(); strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
