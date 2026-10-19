// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { Icon } from '@iconify/react';
import Box from '@mui/material/Box';
import Checkbox from '@mui/material/Checkbox';
import Chip from '@mui/material/Chip';
import IconButton from '@mui/material/IconButton';
import InputAdornment from '@mui/material/InputAdornment';
import ListItemText from '@mui/material/ListItemText';
import MenuItem from '@mui/material/MenuItem';
import Select from '@mui/material/Select';
import type { MRT_Column, MRT_TableInstance } from 'material-react-table';
import { useState } from 'react';
import { WorkloadRow } from '../../hooks/useWorkloadRows/workloadRow.types';

// Replaces the table's own multi-select filter, which lists the faceted
// values of the rows. The host turns those off above 500 rows, and the menu
// then opens empty. The options are read from the table's rows when the menu
// opens instead, at any size, as the header cell holding this filter is not
// re-rendered when the rows change.
//
// The filter value also outlives the rows (it is not reset when they change),
// so a selection whose rows are gone, for example after switching the
// namespace selector, would leave an empty table and a blank chip with
// nothing to uncheck. The selected values are kept in the options.
export function SelectFilter({
  column,
  table,
}: {
  column: MRT_Column<WorkloadRow>;
  table: MRT_TableInstance<WorkloadRow>;
}) {
  const [open, setOpen] = useState(false);
  const selected = (column.getFilterValue() as string[] | undefined) ?? [];
  const options = open
    ? [
        ...new Set([
          ...table.getCoreRowModel().flatRows.map(row => String(row.getValue(column.id))),
          ...selected,
        ]),
      ].sort()
    : selected;
  const header = String(column.columnDef.header);
  const { clearFilter } = table.options.localization;

  return (
    <Select
      multiple
      displayEmpty
      variant="standard"
      size="small"
      fullWidth
      open={open}
      onOpen={() => setOpen(true)}
      onClose={() => setOpen(false)}
      value={selected}
      onChange={event => {
        const next = event.target.value as string[];
        column.setFilterValue(next.length ? next : undefined);
      }}
      // Chips that wrap, as the status filter shows its picks, so many picked
      // values grow the cell in height rather than running past its width.
      renderValue={values =>
        values.length ? (
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5 }}>
            {values.map(value => (
              <Chip key={value} label={value} size="small" />
            ))}
          </Box>
        ) : (
          `Filter by ${header}`
        )
      }
      endAdornment={
        selected.length ? (
          // Clear of the dropdown arrow, which sits at the right edge. The
          // mouse down is stopped so that pressing it does not open the menu.
          <InputAdornment position="end" sx={{ mr: 3 }}>
            <IconButton
              size="small"
              aria-label={clearFilter}
              onMouseDown={event => event.stopPropagation()}
              onClick={() => column.setFilterValue(undefined)}
            >
              <Icon icon="mdi:close" />
            </IconButton>
          </InputAdornment>
        ) : null
      }
      sx={{ '& .MuiSelect-select': { height: 'auto', whiteSpace: 'normal' } }}
      inputProps={{ 'aria-label': `Filter by ${header}` }}
    >
      {options.map(option => (
        <MenuItem key={option} value={option} dense>
          <Checkbox size="small" checked={selected.includes(option)} />
          <ListItemText primary={option} />
        </MenuItem>
      ))}
    </Select>
  );
}
