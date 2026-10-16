import { Group, MultiSelect, Paper, SegmentedControl, Stack } from '@mantine/core';
import { DatePickerInput } from '@mantine/dates';
import { IconCalendar } from '@tabler/icons-react';
import type { Category, User } from '../api';
import { periodOptions, presetRange, type PanelState, type PeriodPreset } from '../filters';

interface Props {
  state: PanelState;
  users: User[];
  categories: Category[];
  onChange: (patch: Partial<PanelState>) => void;
}

export function FiltersBar({ state, users, categories, onChange }: Props) {
  const { filters, period } = state;

  const setPeriod = (p: PeriodPreset) => {
    if (p === 'custom') {
      onChange({ period: p });
    } else {
      onChange({ period: p, filters: { ...filters, ...presetRange(p) } });
    }
  };

  return (
    <Paper withBorder p="md">
      <Stack gap="sm">
        <SegmentedControl
          value={period}
          onChange={(v) => setPeriod(v as PeriodPreset)}
          data={periodOptions}
          fullWidth
          styles={{ root: { overflowX: 'auto' } }}
        />
        <Group grow align="flex-start" wrap="wrap" gap="sm">
          {period === 'custom' && (
            <DatePickerInput
              type="range"
              label="Период"
              placeholder="Выберите даты"
              leftSection={<IconCalendar size={16} />}
              value={[filters.from, filters.to]}
              onChange={([from, to]) => onChange({ filters: { ...filters, from, to } })}
              valueFormat="DD.MM.YYYY"
              clearable
              miw={220}
            />
          )}
          <MultiSelect
            label="Член семьи"
            placeholder={filters.users.length ? undefined : 'Все'}
            data={users.map((u) => ({ value: String(u.telegram_id), label: u.name }))}
            value={filters.users}
            onChange={(v) => onChange({ filters: { ...filters, users: v } })}
            clearable
            searchable
            nothingFoundMessage="Никого не нашлось"
            miw={220}
          />
          <MultiSelect
            label="Категория"
            placeholder={filters.categories.length ? undefined : 'Все'}
            data={categories.map((c) => ({ value: c.code, label: c.name }))}
            value={filters.categories}
            onChange={(v) => onChange({ filters: { ...filters, categories: v } })}
            clearable
            miw={220}
          />
        </Group>
      </Stack>
    </Paper>
  );
}
