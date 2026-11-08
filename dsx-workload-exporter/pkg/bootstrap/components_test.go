// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package bootstrap_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/bootstrap"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/config"
)

type warningRecorder struct {
	warnings chan string
}

var _ = Describe("exporter construction", func() {
	It("constructs real components without starting API requests or mutating client settings", func() {
		cfg := config.DefaultConfig()
		client := &rest.Config{Host: "https://example.test", QPS: 3, Burst: 5}
		components, err := bootstrap.New(cfg, client)
		Expect(err).NotTo(HaveOccurred())
		Expect(components.Runtime).NotTo(BeNil())
		Expect(components.Definitions).NotTo(BeNil())
		Expect(components.Store).NotTo(BeNil())
		Expect(components.Runtime.Ready()).To(BeFalse())
		Expect(client.QPS).To(Equal(float32(3)))
		Expect(client.Burst).To(Equal(5))
		Expect(client.RateLimiter).To(BeNil())
		Expect(client.WarningHandler).To(BeNil())
		Expect(client.WarningHandlerWithContext).To(BeNil())
	})

	It("rejects missing client configuration and invalid runtime settings", func() {
		cfg := config.DefaultConfig()
		_, err := bootstrap.New(cfg, nil)
		Expect(err).To(MatchError("kubernetes configuration is required"))
		cfg.RequestTimeout = "-1s"
		_, err = bootstrap.New(cfg, &rest.Config{Host: "https://example.test"})
		Expect(err).To(HaveOccurred())
	})

	for _, handler := range []string{"default", "custom", "custom with context"} {
		It("handles API warnings with the "+handler+" handler", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Add("Warning", `299 kube-apiserver "deprecated API"`)
				w.Header().Add("Warning", `299 kube-apiserver "deprecated API"`)
				w.Header().Add("Warning", `299 kube-apiserver "another API warning"`)
				if r.URL.Query().Get("sendInitialEvents") == "true" {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"BadRequest","code":400}`)
					return
				}
				if r.URL.Query().Get("watch") == "true" {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				fmt.Fprint(w, `{"apiVersion":"run.ai/v1alpha1","kind":"KartaList","metadata":{"resourceVersion":"1"},"items":[]}`)
			}))
			defer server.Close()
			client := &rest.Config{Host: server.URL}
			recorder := warningRecorder{warnings: make(chan string, 20)}
			switch handler {
			case "custom":
				client.WarningHandler = recorder
			case "custom with context":
				client.WarningHandlerWithContext = recorder
			}
			output, err := os.CreateTemp(GinkgoT().TempDir(), "warnings")
			Expect(err).NotTo(HaveOccurred())
			defer output.Close()
			components, err := func() (app.Components, error) {
				stderr := os.Stderr
				os.Stderr = output
				defer func() { os.Stderr = stderr }()
				return bootstrap.New(config.DefaultConfig(), client)
			}()
			Expect(err).NotTo(HaveOccurred())

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- components.Definitions.Run(ctx) }()
			err = components.Definitions.WaitLoaded(ctx)
			cancel()
			Expect(<-done).To(Succeed())
			Expect(err).NotTo(HaveOccurred())
			warnings, err := os.ReadFile(output.Name())
			Expect(err).NotTo(HaveOccurred())
			if handler == "default" {
				Expect(string(warnings)).To(Equal("Warning: deprecated API\nWarning: another API warning\n"))
				Expect(client.WarningHandler).To(BeNil())
				Expect(client.WarningHandlerWithContext).To(BeNil())
			} else {
				Expect(warnings).To(BeEmpty())
				Expect(recorder.warnings).To(Receive(Equal("deprecated API")))
			}
		})
	}
})

func (r warningRecorder) HandleWarningHeader(_ int, _ string, message string) {
	r.warnings <- message
}

func (r warningRecorder) HandleWarningHeaderWithContext(_ context.Context, _ int, _ string, message string) {
	r.warnings <- message
}
