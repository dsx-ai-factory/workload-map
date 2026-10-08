// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { Icon } from '@iconify/react';
import IconButton from '@mui/material/IconButton';
import InputAdornment from '@mui/material/InputAdornment';
import TextField from '@mui/material/TextField';
import type { MRT_Column } from 'material-react-table';
import { WorkloadRow } from '../../hooks/useWorkloadRows/workloadRow.types';

// Replaces the table's own text filter, which applies what is typed after a
// debounce and does not cancel it when the clear button is pressed: a pending
// call lands after the clear and puts the old text back, leaving the rows
// filtered and highlighted under an empty box. The rows are already in
// memory, so this applies each keystroke at once.
export function WorkloadNameFilter({ column }: { column: MRT_Column<WorkloadRow> }) {
  const value = (column.getFilterValue() as string | undefined) ?? '';

  return (
    <TextField
      variant="standard"
      size="small"
      fullWidth
      placeholder="Filter by Workload"
      value={value}
      onChange={event => column.setFilterValue(event.target.value || undefined)}
      InputProps={{
        endAdornment: value ? (
          <InputAdornment position="end">
            <IconButton
              size="small"
              aria-label="Clear filter"
              onClick={() => column.setFilterValue(undefined)}
            >
              <Icon icon="mdi:close" />
            </IconButton>
          </InputAdornment>
        ) : null,
      }}
    />
  );
}
