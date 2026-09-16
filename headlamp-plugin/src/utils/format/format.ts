// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Shared formatters for resource-table cells — not workload-specific, so any
// page rendering pod counts or CPU/memory quantities can reuse these.

export function formatPods(ready: number | null, desired: number | null): string {
  if (ready === null && desired === null) {
    return 'n/a';
  }
  return `${ready ?? '-'}/${desired ?? '-'}`;
}

export function formatCount(value: number | null): string {
  return value === null ? 'n/a' : String(value);
}

export function formatCpuMillis(millis: number | null): string {
  if (millis === null) {
    return 'n/a';
  }
  return millis % 1000 === 0 ? String(millis / 1000) : `${millis}m`;
}

export function formatMemoryBytes(bytes: number | null): string {
  if (bytes === null) {
    return 'n/a';
  }
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex++;
  }
  return `${Number.isInteger(value) ? value : value.toFixed(1)} ${units[unitIndex]}`;
}
