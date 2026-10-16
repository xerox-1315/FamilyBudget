import { useCallback, useState } from 'react';
import dayjs from 'dayjs';
import type { Filters } from './api';

export type PeriodPreset = 'week' | 'month' | 'prev_month' | 'last30' | 'all' | 'custom';

export const periodOptions: { value: PeriodPreset; label: string }[] = [
  { value: 'week', label: 'Эта неделя' },
  { value: 'month', label: 'Этот месяц' },
  { value: 'prev_month', label: 'Прошлый месяц' },
  { value: 'last30', label: '30 дней' },
  { value: 'all', label: 'Всё время' },
  { value: 'custom', label: 'Период' },
];

const fmt = (d: dayjs.Dayjs) => d.format('YYYY-MM-DD');

export function presetRange(p: PeriodPreset): { from: string | null; to: string | null } {
  const today = dayjs();
  switch (p) {
    case 'week': {
      const monday = today.subtract((today.day() + 6) % 7, 'day');
      return { from: fmt(monday), to: fmt(today) };
    }
    case 'month':
      return { from: fmt(today.startOf('month')), to: fmt(today) };
    case 'prev_month': {
      const prev = today.subtract(1, 'month');
      return { from: fmt(prev.startOf('month')), to: fmt(prev.endOf('month')) };
    }
    case 'last30':
      return { from: fmt(today.subtract(29, 'day')), to: fmt(today) };
    default:
      return { from: null, to: null };
  }
}

export interface PanelState {
  period: PeriodPreset;
  filters: Filters;
  page: number;
}

const list = (v: string | null) => (v ? v.split(',').filter(Boolean) : []);

function readUrl(): PanelState {
  const q = new URLSearchParams(window.location.search);
  const period = (periodOptions.some((o) => o.value === q.get('period'))
    ? q.get('period')
    : 'month') as PeriodPreset;
  const range = period === 'custom' ? { from: q.get('from'), to: q.get('to') } : presetRange(period);
  return {
    period,
    filters: { users: list(q.get('users')), categories: list(q.get('categories')), ...range },
    page: Math.max(1, Number(q.get('page')) || 1),
  };
}

function writeUrl(s: PanelState) {
  const q = new URLSearchParams();
  if (s.period !== 'month') q.set('period', s.period);
  if (s.period === 'custom') {
    if (s.filters.from) q.set('from', s.filters.from);
    if (s.filters.to) q.set('to', s.filters.to);
  }
  if (s.filters.users.length) q.set('users', s.filters.users.join(','));
  if (s.filters.categories.length) q.set('categories', s.filters.categories.join(','));
  if (s.page > 1) q.set('page', String(s.page));
  const search = q.toString();
  window.history.replaceState(null, '', search ? `?${search}` : window.location.pathname);
}

/** Panel filters kept in the URL, so a filtered view survives reloads and can be shared. */
export function usePanelState() {
  const [state, setState] = useState<PanelState>(readUrl);
  const update = useCallback((patch: Partial<PanelState>) => {
    setState((prev) => {
      const next = { ...prev, ...patch, filters: { ...prev.filters, ...patch.filters } };
      if (!('page' in patch)) next.page = 1;
      writeUrl(next);
      return next;
    });
  }, []);
  return [state, update] as const;
}
