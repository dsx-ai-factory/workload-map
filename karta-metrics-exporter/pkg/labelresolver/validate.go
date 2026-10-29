// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package labelresolver

import (
	"fmt"
	"regexp"
	"strings"
)

var labelName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ValidateConfig checks rule structure without compiling or evaluating expressions.
func ValidateConfig(cfg Config) error {
	for i, rule := range cfg.Rules {
		if !labelName.MatchString(rule.Name) || strings.HasPrefix(rule.Name, "__") {
			return fmt.Errorf("labelRules[%d]: invalid label name %q", i, rule.Name)
		}
		if len(rule.Metrics) == 0 || len(rule.Sources) == 0 || strings.TrimSpace(rule.Expression) == "" {
			return fmt.Errorf("labelRules[%d]: metrics, sources and expression are required", i)
		}
		sources := make(map[Source]bool)
		for _, source := range rule.Sources {
			if (source != SourceWorkload && source != SourcePod) || sources[source] {
				return fmt.Errorf("labelRules[%d]: invalid or duplicate source %q", i, source)
			}
			sources[source] = true
		}
		metrics := make(map[string]bool)
		for _, metric := range rule.Metrics {
			allowsPod, ok := supportedMetrics[metric]
			if !ok {
				return fmt.Errorf("labelRules[%d]: unsupported metric %q", i, metric)
			}
			if sources[SourcePod] && !allowsPod {
				return fmt.Errorf("labelRules[%d]: %s cannot use a Pod source", i, metric)
			}
			if metrics[metric] {
				return fmt.Errorf("labelRules[%d]: duplicate metric %q", i, metric)
			}
			metrics[metric] = true
		}
	}
	return nil
}
