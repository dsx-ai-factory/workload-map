#!/usr/bin/env node
// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// The update wrappers already declare each upstream release policy. Capture their
// checker arguments without contacting upstream or building the Go checker.
process.stdout.write(JSON.stringify(process.argv.slice(2)));
