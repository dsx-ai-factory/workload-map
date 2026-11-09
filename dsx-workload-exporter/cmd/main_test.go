// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestMain runs the command in a child process for startup checks.
func TestMain(m *testing.M) {
	if os.Getenv("KARTA_TEST_COMMAND_PROCESS") == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				os.Args = append([]string{"dsx-workload-exporter"}, os.Args[i+1:]...)
				main()
				os.Exit(0)
			}
		}
		os.Exit(2)
	}
	os.Exit(m.Run())
}

var _ = Describe("startup rejects invalid expressions", func() {
	for _, tc := range []struct{ name, expression, limits, want string }{
		{"syntax", `object[`, "", "compiling label expression"},
		{"non string", `1 + 1`, "", "must produce a string"},
		{"expression limit", `object.kind`, "genericLabelEvaluationLimits:\n  maxExpressionBytes: 4\n", "exceeds the maximum length"},
	} {
		It(tc.name, func(specCtx SpecContext) {
			path := filepath.Join(GinkgoT().TempDir(), "config.yaml")
			contents := "labelRules:\n- name: team\n  metrics: [dsx_workload_status]\n  sources: [workload]\n  expression: '" + tc.expression + "'\n" + tc.limits
			Expect(os.WriteFile(path, []byte(contents), 0o600)).To(Succeed())
			ctx, cancel := context.WithTimeout(specCtx, 5*time.Second)
			defer cancel()
			output, err := runExporterCommand(ctx, "--config", path)
			Expect(ctx.Err()).To(Succeed(), "command waited for a signal instead of rejecting configuration")
			var exit *exec.ExitError
			Expect(errors.As(err, &exit)).To(BeTrue())
			Expect(exit.ExitCode()).To(Equal(1))
			Expect(string(output)).To(ContainSubstring(tc.want))
		})
	}
})

var _ = Describe("command help", func() {
	It("documents only the three public generic evaluation limits", func(specCtx SpecContext) {
		ctx, cancel := context.WithTimeout(specCtx, 5*time.Second)
		defer cancel()
		output, err := runExporterCommand(ctx, "--help")
		Expect(err).To(Succeed(), string(output))
		Expect(string(output)).To(ContainSubstring("metrics-bind-address"))
		Expect(string(output)).To(ContainSubstring("pprof-bind-address"))
		for _, name := range []string{
			"generic-label-eval-cost-limit", "generic-label-eval-max-expression-bytes", "generic-label-eval-max-result-bytes",
			"DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_COST_LIMIT", "DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_MAX_EXPRESSION_BYTES", "DSX_WORKLOAD_EXPORTER_GENERIC_LABEL_EVAL_MAX_RESULT_BYTES",
		} {
			Expect(string(output)).To(ContainSubstring(name))
		}
		for _, removed := range []string{"interrupt", "INTERRUPT", "-label-cost-limit", "-label-max-", "labelLimits", "-cel-", "_CEL_"} {
			Expect(string(output)).NotTo(ContainSubstring(removed))
		}
	})
})

var _ = Describe("pprof server", func() {
	It("serves the profile handlers for GET and HEAD only, and nothing else", func() {
		handler := newPprofServer("127.0.0.1:0").Handler
		for _, tc := range []struct {
			method, path string
			want         int
		}{
			{http.MethodGet, "/debug/pprof/", http.StatusOK},
			{http.MethodGet, "/debug/pprof/goroutine", http.StatusOK},
			{http.MethodGet, "/debug/pprof/heap", http.StatusOK},
			{http.MethodHead, "/debug/pprof/heap", http.StatusOK},
			// A POST must not start a CPU profile or read symbols.
			{http.MethodPost, "/debug/pprof/profile", http.StatusMethodNotAllowed},
			{http.MethodPost, "/debug/pprof/symbol", http.StatusMethodNotAllowed},
			{http.MethodGet, "/metrics", http.StatusNotFound},
		} {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))
			Expect(recorder.Code).To(Equal(tc.want), tc.method+" "+tc.path)
			if tc.want == http.StatusMethodNotAllowed {
				Expect(recorder.Header().Get("Allow")).To(Equal("GET, HEAD"))
			}
		}
	})
})

func runExporterCommand(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestCommand$", "--"}, args...)...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DSX_WORKLOAD_EXPORTER_") && !strings.HasPrefix(entry, "KARTA_TEST_COMMAND_PROCESS=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "KARTA_TEST_COMMAND_PROCESS=1")
	return cmd.CombinedOutput()
}
