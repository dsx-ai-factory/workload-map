// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { SectionBox, SectionFilterHeader } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import { WorkloadsTable } from '../../components/workloadsTable/WorkloadsTable';
import { useWorkloadRows } from '../../hooks/useWorkloadRows/useWorkloadRows';

// Route target for /karta/workloads: owns fetching, delegates rendering.
export function WorkloadsPage() {
  const {
    rows,
    loading,
    error,
    engineError,
    retryEngine,
    errorsByKind,
    errorsByCluster,
    warningsByCluster,
    fetchers,
  } = useWorkloadRows();
  // Only a cluster that produced nothing explains the kinds missing under it.
  // A cluster that still has rows does not, so its kinds report for themselves.
  const failures = [
    // Rows are metadata and survive without the engine, so its failure costs
    // the status column rather than the table.
    ...Object.entries(errorsByCluster).map(([cluster, clusterError]) => ({
      key: cluster,
      message: `Unable to read cluster ${cluster}: ${clusterError.message}`,
    })),
    ...Object.entries(warningsByCluster).map(([cluster, warning]) => ({
      key: `${cluster}-warning`,
      message: `Partial results for cluster ${cluster}: ${warning.message}`,
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
      {engineError && (
        // Nothing announces that a failed download could now succeed, so the
        // retry is the user's to ask for.
        <Alert
          severity="warning"
          sx={{ mb: 1 }}
          action={
            <Button color="inherit" size="small" onClick={retryEngine}>
              Retry
            </Button>
          }
        >
          {`Status unavailable: ${engineError.message}`}
        </Alert>
      )}
      {failures.map(failure => (
        <Alert key={failure.key} severity="warning" sx={{ mb: 1 }}>
          {failure.message}
        </Alert>
      ))}
      <WorkloadsTable rows={rows} loading={loading} errorMessage={error?.message} />
    </SectionBox>
  );
}
