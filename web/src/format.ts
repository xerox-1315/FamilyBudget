import dayjs from 'dayjs';
import type { CategoryCode } from './api';

const rub = new Intl.NumberFormat('ru-RU', {
  style: 'currency',
  currency: 'RUB',
  minimumFractionDigits: 0,
  maximumFractionDigits: 2,
});

/** Formats kopecks as rubles: 120050 → "1 200,5 ₽". */
export function formatMoney(kopecks: number): string {
  return rub.format(kopecks / 100);
}

/** Converts a ruble amount to integer kopecks without float drift. */
export function rublesToKopecks(value: number | string): number {
  const s = typeof value === 'number' ? value.toFixed(2) : value.replace(',', '.').trim();
  const [int, frac = ''] = s.split('.');
  return Number(int) * 100 + Number((frac + '00').slice(0, 2));
}

export function formatDate(iso: string): string {
  return dayjs(iso).format('DD.MM.YYYY');
}

export const categoryColors: Record<CategoryCode, string> = {
  food: 'orange.6',
  transport: 'blue.6',
  housing: 'grape.6',
  entertainment: 'pink.6',
  health: 'teal.6',
  shopping: 'yellow.6',
  other: 'gray.6',
};

/** Mantine color name without the shade, for Badge and similar components. */
export function categoryColor(code: CategoryCode): string {
  return categoryColors[code].split('.')[0];
}
