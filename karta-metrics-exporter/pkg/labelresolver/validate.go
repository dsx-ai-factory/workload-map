// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package labelresolver

import (
	"fmt"
	"regexp"
	"strings"
)

var labelName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

var reservedLabels = map[string]bool{
	"namespace": true, "workload_name": true, "workload_group": true,
	"workload_version": true, "workload_kind": true, "workload_uid": true,
	"owner_resolution": true, "pod_name": true, "pod_uid": true,
	"component": true, "component_instance": true, "replica_group_id": true,
	"observation_valid": true, "phase": true, "karta": true, "karta_source": true,
	"job": true, "instance": true,
}

// ValidateConfig checks rule structure without compiling or evaluating expressions.
func ValidateConfig(cfg Config) error {
	type metricLabel struct {
		metric string
		label  string
	}
	seen := make(map[metricLabel]bool)
	for i, rule := range cfg.Rules {
		if !labelName.MatchString(rule.Name) || strings.HasPrefix(rule.Name, "__") || reservedLabels[rule.Name] {
			return fmt.Errorf("labelRules[%d]: invalid or reserved label name %q", i, rule.Name)
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
		for _, metric := range rule.Metrics {
			allowsPod, ok := supportedMetrics[metric]
			if !ok {
				return fmt.Errorf("labelRules[%d]: unsupported metric %q", i, metric)
			}
			if sources[SourcePod] && !allowsPod {
				return fmt.Errorf("labelRules[%d]: %s cannot use a Pod source", i, metric)
			}
			key := metricLabel{metric: metric, label: rule.Name}
			if seen[key] {
				return fmt.Errorf("labelRules[%d]: duplicate label %q for %s", i, rule.Name, metric)
			}
			seen[key] = true
		}
	}
	return nil
}
