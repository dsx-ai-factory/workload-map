// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package recorder drives a workload through a declared flow and records the CRs it passes through, for the
// replay tests to check against Karta. It judges each state from the workload's own fields and never runs
// Karta, and depends on no test framework: failures come back as errors, progress goes to an injected writer.
package recorder
