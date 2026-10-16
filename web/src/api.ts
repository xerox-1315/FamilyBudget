// Typed client for the backend REST API. Amounts are integer kopecks, dates are YYYY-MM-DD.

export type CategoryCode =
  | 'food'
  | 'transport'
  | 'housing'
  | 'entertainment'
  | 'health'
  | 'shopping'
  | 'other';

export interface Category {
  code: CategoryCode;
  name: string;
}

export interface User {
  telegram_id: number;
  /** What to show: the chosen name, or the Telegram one. */
  name: string;
  /** Chosen name; empty when not set. */
  custom_name: string;
  telegram_name: string;
  username: string;
  is_member: boolean;
  joined_at: string | null;
}

export interface Invite {
  code: string;
  /** Empty when the Telegram bot is not configured. */
  link: string;
  bot_username: string;
}

export interface Expense {
  id: number;
  user_id: number;
  user_name: string;
  amount: number;
  category: CategoryCode;
  category_name: string;
  spent_at: string;
  comment: string;
  created_at: string;
  updated_at: string;
}

export interface ExpenseInput {
  user_id: number;
  amount: number;
  category: CategoryCode;
  spent_at: string;
  comment: string;
}

export interface ExpenseList {
  items: Expense[];
  total: number;
  total_amount: number;
}

export interface Summary {
  total_amount: number;
  count: number;
  by_category: { category: CategoryCode; name: string; amount: number; count: number }[];
}

export interface Filters {
  users: string[];
  categories: string[];
  from: string | null;
  to: string | null;
}

export class ApiError extends Error {}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, {
    ...init,
    headers: init?.body ? { 'Content-Type': 'application/json' } : undefined,
  });
  if (!resp.ok) {
    let message = `Ошибка ${resp.status}`;
    try {
      const body = await resp.json();
      if (body?.error) message = body.error;
    } catch {
      // keep the generic message
    }
    throw new ApiError(message);
  }
  if (resp.status === 204) return undefined as T;
  return resp.json() as Promise<T>;
}

function filterQuery(f: Filters, extra: Record<string, string | number> = {}): string {
  const q = new URLSearchParams();
  if (f.users.length) q.set('user_id', f.users.join(','));
  if (f.categories.length) q.set('category', f.categories.join(','));
  if (f.from) q.set('from', f.from);
  if (f.to) q.set('to', f.to);
  for (const [k, v] of Object.entries(extra)) q.set(k, String(v));
  const s = q.toString();
  return s ? `?${s}` : '';
}

export const api = {
  categories: () => request<Category[]>('/api/categories'),
  users: () => request<User[]>('/api/users'),
  rename: (id: number, name: string) =>
    request<User>(`/api/users/${id}`, { method: 'PATCH', body: JSON.stringify({ name }) }),
  exclude: (id: number) => request<void>(`/api/users/${id}/membership`, { method: 'DELETE' }),
  invite: () => request<Invite>('/api/invite'),
  rotateInvite: () => request<Invite>('/api/invite/rotate', { method: 'POST' }),
  expenses: (f: Filters, limit: number, offset: number) =>
    request<ExpenseList>(`/api/expenses${filterQuery(f, { limit, offset })}`),
  summary: (f: Filters) => request<Summary>(`/api/expenses/summary${filterQuery(f)}`),
  create: (e: ExpenseInput) =>
    request<Expense>('/api/expenses', { method: 'POST', body: JSON.stringify(e) }),
  update: (id: number, e: Omit<ExpenseInput, 'user_id'>) =>
    request<Expense>(`/api/expenses/${id}`, { method: 'PUT', body: JSON.stringify(e) }),
  remove: (id: number) => request<void>(`/api/expenses/${id}`, { method: 'DELETE' }),
};
