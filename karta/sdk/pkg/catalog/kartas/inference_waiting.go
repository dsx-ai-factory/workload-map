// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package kartas

// Empty status on an existing object means the first controller observation is pending.
// Any reported status field leaves this branch so unknown states and errors stay visible.
const inferenceEmptyStatusExpression = `.metadata.deletionTimestamp == null and
    (.metadata.generation // 0) > 0 and (.status == null or .status == {})`
