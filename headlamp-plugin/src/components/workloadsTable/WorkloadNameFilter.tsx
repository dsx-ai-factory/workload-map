// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { Icon } from '@iconify/react';
import IconButton from '@mui/material/IconButton';
import InputAdornment from '@mui/material/InputAdornment';
import TextField from '@mui/material/TextField';
import type { MRT_Column, MRT_TableInstance } from 'material-react-table';
import { WorkloadRow } from '../../hooks/useWorkloadRows/workloadRow.types';

// Replaces the table's own text filter, which applies what is typed after a
// debounce and does not cancel it when the clear button is pressed: a pending
// call lands after the clear and puts the old text back, leaving the rows
// filtered and highlighted under an empty box. The rows are already in
// memory, so this applies each keystroke at once.
export function WorkloadNameFilter({
  column,
  table,
}: {
  column: MRT_Column<WorkloadRow>;
  table: MRT_TableInstance<WorkloadRow>;
}) {
  const value = (column.getFilterValue() as string | undefined) ?? '';
  // The table's own, translated strings, as its stock filter field uses.
  const { filterByColumn, clearFilter } = table.options.localization;
  const label = filterByColumn.replace('{column}', String(column.columnDef.header));

  return (
    <TextField
      variant="standard"
      size="small"
      fullWidth
      placeholder={label}
      // Registered so the filter icon in the header focuses the box.
      inputRef={element => {
        table.refs.filterInputRefs.current[`${column.id}-0`] = element;
      }}
      inputProps={{ 'aria-label': label }}
      value={value}
      onChange={event => column.setFilterValue(event.target.value || undefined)}
      InputProps={{
        endAdornment: value ? (
          <InputAdornment position="end">
            <IconButton
              size="small"
              aria-label={clearFilter}
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
