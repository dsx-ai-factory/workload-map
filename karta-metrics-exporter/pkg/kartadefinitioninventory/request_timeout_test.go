// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartadefinitioninventory

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

type timeoutTransport func(*http.Request) (*http.Response, error)

func (f timeoutTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestRejectNegativeRequestTimeout(t *testing.T) {
	_, err := New(nil, Config{RequestTimeout: -time.Second})
	require.ErrorIs(t, err, ErrInvalidConfig)
}

func TestListPagesRespectRequestTimeoutAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name     string
		page     string
		canceled bool
	}{
		{name: "first page"},
		{name: "continuation page", page: "next"},
		{name: "caller cancellation", canceled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			started := make(chan *http.Request, 1)
			client, err := dynamic.NewForConfig(&rest.Config{Host: "https://example.test", Transport: timeoutTransport(func(request *http.Request) (*http.Response, error) {
				started <- request
				<-request.Context().Done()
				return nil, request.Context().Err()
			})})
			require.NoError(t, err)
			timeout := 25 * time.Millisecond
			if test.canceled {
				timeout = time.Hour
			}
			inventory, err := New(client, Config{Cluster: SourceConfig{Enabled: true}, RequestTimeout: timeout})
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := inventory.list(ctx, client.Resource(kartasGVR), metav1.ListOptions{Continue: test.page})
				done <- err
			}()
			var request *http.Request
			select {
			case request = <-started:
			case <-time.After(time.Second):
				t.Fatal("LIST did not start")
			}
			require.Equal(t, test.page, request.URL.Query().Get("continue"))
			want := context.DeadlineExceeded
			if test.canceled {
				cancel()
				want = context.Canceled
			}
			select {
			case err := <-done:
				require.ErrorIs(t, err, want)
			case <-time.After(time.Second):
				t.Fatal("LIST did not honor its request context")
			}
			require.ErrorIs(t, request.Context().Err(), want)
		})
	}
}

func TestListTimeoutDoesNotCancelHealthyWatch(t *testing.T) {
	listContexts := make(chan context.Context, 1)
	watchContexts := make(chan context.Context, 1)
	reader, writer := io.Pipe()
	defer writer.Close()
	client, err := dynamic.NewForConfig(&rest.Config{Host: "https://example.test", Transport: timeoutTransport(func(request *http.Request) (*http.Response, error) {
		body := io.NopCloser(strings.NewReader(`{"apiVersion":"run.ai/v1alpha1","kind":"KartaList","metadata":{"resourceVersion":"1"},"items":[]}`))
		if request.URL.Query().Get("watch") == "true" {
			watchContexts <- request.Context()
			body = reader
		} else {
			listContexts <- request.Context()
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: body, Request: request}, nil
	})})
	require.NoError(t, err)
	timeout := 25 * time.Millisecond
	inventory, err := New(client, Config{Cluster: SourceConfig{Enabled: true}, RequestTimeout: timeout})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resource := client.Resource(kartasGVR)
	_, err = inventory.list(ctx, resource, metav1.ListOptions{})
	require.NoError(t, err)
	require.ErrorIs(t, (<-listContexts).Err(), context.Canceled)
	stream, err := inventory.startWatch(ctx, resource, metav1.ListOptions{ResourceVersion: "1"})
	require.NoError(t, err)
	defer stream.Stop()
	requestContext := <-watchContexts
	_, bounded := requestContext.Deadline()
	require.False(t, bounded)
	select {
	case <-stream.ResultChan():
		t.Fatal("healthy WATCH ended before the LIST timeout elapsed")
	case <-time.After(3 * timeout):
	}
	require.NoError(t, requestContext.Err())
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer, "{\"type\":\"ADDED\",\"object\":{\"apiVersion\":\"run.ai/v1alpha1\",\"kind\":\"Karta\",\"metadata\":{\"name\":\"job\",\"resourceVersion\":\"2\"}}}\n")
		written <- err
	}()
	select {
	case event := <-stream.ResultChan():
		require.Equal(t, watch.Added, event.Type)
	case <-time.After(time.Second):
		t.Fatal("WATCH did not deliver an event after the LIST timeout")
	}
	require.NoError(t, <-written)
}
