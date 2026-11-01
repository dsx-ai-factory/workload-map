// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	kubediscovery "k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

type mappingTransport func(*http.Request) (*http.Response, error)

func (f mappingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func blockingDiscoveryMapper(t *testing.T, entered chan<- context.Context) meta.RESTMapper {
	t.Helper()
	client, err := kubediscovery.NewDiscoveryClientForConfig(&rest.Config{
		Host: "https://example.test",
		Transport: mappingTransport(func(request *http.Request) (*http.Response, error) {
			select {
			case entered <- request.Context():
			default:
			}
			<-request.Context().Done()
			return nil, request.Context().Err()
		}),
	})
	require.NoError(t, err)
	return restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(client))
}

func TestDiscoveryMappingRequestTimeout(t *testing.T) {
	for _, path := range []string{"source watch", "resource filter"} {
		t.Run(path, func(t *testing.T) {
			r, _ := simpleRuntime(t, nil)
			entered := make(chan context.Context, 1)
			r.state.reader.clients.Mapper = blockingDiscoveryMapper(t, entered)
			r.state.reader.options.RequestTimeout = 25 * time.Millisecond
			r.state.config.ResourceRules = []ResourceRule{{APIGroups: []string{"*"}, APIVersions: []string{"*"}, Resources: []string{"*"}}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				switch path {
				case "source watch":
					_, err = r.state.ensureWatch(ctx, podGVK, "", false)
				case "resource filter":
					_, err = r.state.isResourceSelected(ctx, podGVK)
				}
				done <- err
			}()
			select {
			case requestCtx := <-entered:
				deadline, ok := requestCtx.Deadline()
				require.True(t, ok)
				require.LessOrEqual(t, time.Until(deadline), r.state.reader.options.RequestTimeout)
			case <-time.After(time.Second):
				t.Fatal("discovery request did not start")
			}
			select {
			case err := <-done:
				require.ErrorIs(t, err, context.DeadlineExceeded)
			case <-time.After(time.Second):
				t.Fatal("mapping exceeded the configured request timeout")
			}
		})
	}
}

func TestRuntimeCancellationStopsDiscoveryMapping(t *testing.T) {
	r, _ := simpleRuntime(t, nil)
	entered := make(chan context.Context, 1)
	r.state.reader.clients.Mapper = blockingDiscoveryMapper(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	var requestCtx context.Context
	select {
	case requestCtx = <-entered:
	case <-time.After(time.Second):
		t.Fatal("discovery request did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
		require.ErrorIs(t, requestCtx.Err(), context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop after discovery cancellation")
	}
}
