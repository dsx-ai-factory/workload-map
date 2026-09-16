// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { CommonComponents } from '@kinvolk/headlamp-plugin/lib';
import { WorkloadsTable } from '../../components';
import { useWorkloadRows } from '../../hooks';

const { SectionBox } = CommonComponents;

// WorkloadsPage is the route target for /karta/workloads: it owns the data
// fetching and leaves rendering to WorkloadsTable.
export function WorkloadsPage() {
  const { rows, loading, error, fetchers } = useWorkloadRows();
  // A single kind failing to list (e.g. its CRD isn't installed, or a
  // transient watch error) must not blank rows other kinds already loaded
  // successfully — Headlamp's Table replaces all rows with errorMessage
  // whenever it's set, so only surface it when there's nothing else to show.
  const errorMessage = rows && rows.length > 0 ? null : error?.message;

  return (
    // headerStyle is set explicitly because SectionBox overrides SectionHeader's
    // own 'main' default with 'subsection' for a string title, which would show
    // a smaller heading than every built-in resource list page.
    <SectionBox title="Workloads" headerProps={{ headerStyle: 'main', noPadding: false }}>
      {fetchers}
      <WorkloadsTable rows={rows} loading={loading} errorMessage={errorMessage} />
    </SectionBox>
  );
}
