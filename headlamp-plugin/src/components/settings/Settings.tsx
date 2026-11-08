// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import type { PluginSettingsDetailsProps } from '@kinvolk/headlamp-plugin/lib';
import FormControlLabel from '@mui/material/FormControlLabel';
import Switch from '@mui/material/Switch';

export function Settings({ data, onDataChange }: PluginSettingsDetailsProps) {
  return (
    <FormControlLabel
      label="Enable experimental definitions"
      control={
        <Switch
          checked={data?.enableExperimental === true}
          onChange={(_event, checked) =>
            onDataChange?.({ ...data, enableExperimental: checked })
          }
        />
      }
    />
  );
}
