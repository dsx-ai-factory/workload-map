// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { CommonComponents } from '@kinvolk/headlamp-plugin/lib';
import { SectionFilterHeader } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import Alert from '@mui/material/Alert';
import { WorkloadsTable } from '../../components/workloadsTable/WorkloadsTable';
import { useWorkloadRows } from '../../hooks/useWorkloadRows/useWorkloadRows';

const { SectionBox } = CommonComponents;

// Route target for /karta/workloads: owns fetching, delegates rendering.
export function WorkloadsPage() {
  const { rows, loading, error, errorsByKind, errorsByCluster, fetchers } = useWorkloadRows();
  // Cluster first: an unreachable cluster explains every kind missing under
  // it, so repeating those would bury the line that matters.
  const failures = [
    ...Object.entries(errorsByCluster).map(([cluster, clusterError]) => ({
      key: cluster,
      message: `Unable to read cluster ${cluster}: ${clusterError.message}`,
    })),
    ...Object.entries(errorsByKind)
      .filter(([key]) => !(key.split('/')[0] in errorsByCluster))
      .map(([key, kindError]) => ({
        key,
        message: `Unable to list ${key}: ${kindError.message}`,
      })),
  ];

  return (
    // SectionFilterHeader carries Headlamp's namespace chooser, as its own
    // list pages do.
    <SectionBox
      title={<SectionFilterHeader title="Workloads" headerStyle="main" />}
      headerProps={{ noPadding: false }}
    >
      {fetchers}
      {/* Beside the table, not instead of it: errorMessage hides every row. */}
      {failures.map(failure => (
        <Alert key={failure.key} severity="warning" sx={{ mb: 1 }}>
          {failure.message}
        </Alert>
      ))}
      <WorkloadsTable rows={rows} loading={loading} errorMessage={error?.message} />
    </SectionBox>
  );
}
