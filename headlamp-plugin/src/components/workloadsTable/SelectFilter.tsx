// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import Checkbox from '@mui/material/Checkbox';
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
      renderValue={values => (values.length ? values.join(', ') : `Filter by ${header}`)}
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
