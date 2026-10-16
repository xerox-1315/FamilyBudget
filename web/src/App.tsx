import { useState } from 'react';
import {
  ActionIcon,
  Alert,
  Button,
  Container,
  Group,
  Modal,
  Stack,
  Text,
  Title,
  useComputedColorScheme,
  useMantineColorScheme,
} from '@mantine/core';
import { modals } from '@mantine/modals';
import { notifications } from '@mantine/notifications';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { IconAlertCircle, IconMoon, IconPlus, IconSun, IconUsers } from '@tabler/icons-react';
import { api, type Expense, type ExpenseInput } from './api';
import { usePanelState } from './filters';
import { formatDate, formatMoney } from './format';
import { FiltersBar } from './components/FiltersBar';
import { SummaryPanel } from './components/SummaryPanel';
import { ExpensesTable } from './components/ExpensesTable';
import { ExpenseForm } from './components/ExpenseForm';
import { MembersModal } from './components/MembersModal';

const PAGE_SIZE = 20;

type Editing = { mode: 'create' } | { mode: 'edit'; expense: Expense } | null;

export function App() {
  const [state, update] = usePanelState();
  const [editing, setEditing] = useState<Editing>(null);
  const [membersOpen, setMembersOpen] = useState(false);
  const qc = useQueryClient();
  const { filters, page } = state;

  const users = useQuery({ queryKey: ['users'], queryFn: api.users });
  const categories = useQuery({ queryKey: ['categories'], queryFn: api.categories, staleTime: Infinity });
  const expenses = useQuery({
    queryKey: ['expenses', filters, page],
    queryFn: () => api.expenses(filters, PAGE_SIZE, (page - 1) * PAGE_SIZE),
    placeholderData: keepPreviousData,
  });
  const summary = useQuery({
    queryKey: ['summary', filters],
    queryFn: () => api.summary(filters),
    placeholderData: keepPreviousData,
  });

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['expenses'] });
    qc.invalidateQueries({ queryKey: ['summary'] });
  };
  const fail = (err: Error) => notifications.show({ color: 'red', title: 'Не получилось', message: err.message });

  const save = useMutation({
    mutationFn: (input: ExpenseInput) => {
      if (editing?.mode === 'edit') {
        const { user_id: _ignored, ...rest } = input;
        return api.update(editing.expense.id, rest);
      }
      return api.create(input);
    },
    onSuccess: () => {
      notifications.show({ color: 'teal', message: editing?.mode === 'edit' ? 'Трата сохранена' : 'Трата добавлена' });
      setEditing(null);
      refresh();
    },
    onError: fail,
  });

  const remove = useMutation({
    mutationFn: (e: Expense) => api.remove(e.id),
    onSuccess: () => {
      notifications.show({ color: 'teal', message: 'Трата удалена' });
      // Step back if the last item on the page was removed.
      if (expenses.data?.items.length === 1 && page > 1) update({ page: page - 1 });
      refresh();
    },
    onError: fail,
  });

  const confirmDelete = (e: Expense) =>
    modals.openConfirmModal({
      title: 'Удалить трату?',
      children: (
        <Text size="sm">
          {e.user_name}: {e.comment || e.category_name} — {formatMoney(e.amount)}, {formatDate(e.spent_at)}
        </Text>
      ),
      confirmProps: { color: 'red' },
      onConfirm: () => remove.mutate(e),
    });

  const loadError = users.error || expenses.error || summary.error || categories.error;
  const allUsers = users.data ?? [];
  const members = allUsers.filter((u) => u.is_member);
  const noMembers = users.isSuccess && members.length === 0;

  return (
    <Container size="lg" py="xl">
      <Stack gap="lg">
        <Group justify="space-between" align="flex-end">
          <div>
            <Title order={1} size="h2">
              Семейный бюджет
            </Title>
            <Text c="dimmed" size="sm">
              Траты всех членов семьи из Telegram-бота
            </Text>
          </div>
          <Group gap="xs">
            <ColorSchemeToggle />
            <Button variant="default" leftSection={<IconUsers size={18} />} onClick={() => setMembersOpen(true)}>
              Участники
            </Button>
            <Button leftSection={<IconPlus size={18} />} onClick={() => setEditing({ mode: 'create' })} disabled={noMembers}>
              Добавить трату
            </Button>
          </Group>
        </Group>

        {loadError && (
          <Alert color="red" icon={<IconAlertCircle />} title="Не удалось загрузить данные">
            {loadError.message}
          </Alert>
        )}
        {noMembers && (
          <Alert color="blue" icon={<IconAlertCircle />} title="В семье пока никого нет">
            <Stack gap="xs" align="flex-start">
              <Text size="sm">
                Отправьте членам семьи ссылку-приглашение в бота — и откройте её сами, чтобы тоже вести траты.
              </Text>
              <Button size="xs" leftSection={<IconUsers size={14} />} onClick={() => setMembersOpen(true)}>
                Пригласить
              </Button>
            </Stack>
          </Alert>
        )}

        <FiltersBar state={state} users={allUsers} categories={categories.data ?? []} onChange={update} />
        <SummaryPanel summary={summary.data} loading={summary.isLoading} />
        <ExpensesTable
          items={expenses.data?.items ?? []}
          total={expenses.data?.total ?? 0}
          page={page}
          pageSize={PAGE_SIZE}
          loading={expenses.isLoading}
          onPage={(p) => update({ page: p })}
          onEdit={(e) => setEditing({ mode: 'edit', expense: e })}
          onDelete={confirmDelete}
        />
      </Stack>

      <Modal
        opened={editing !== null}
        onClose={() => setEditing(null)}
        title={editing?.mode === 'edit' ? 'Изменить трату' : 'Новая трата'}
        centered
      >
        {editing && (
          <ExpenseForm
            key={editing.mode === 'edit' ? editing.expense.id : 'new'}
            expense={editing.mode === 'edit' ? editing.expense : undefined}
            users={editing.mode === 'edit' ? allUsers : members}
            categories={categories.data ?? []}
            submitting={save.isPending}
            onSubmit={(input) => save.mutate(input)}
            onCancel={() => setEditing(null)}
          />
        )}
      </Modal>

      <MembersModal opened={membersOpen} onClose={() => setMembersOpen(false)} users={allUsers} />
    </Container>
  );
}

function ColorSchemeToggle() {
  const { setColorScheme } = useMantineColorScheme();
  const scheme = useComputedColorScheme('light');
  return (
    <ActionIcon
      variant="default"
      size="lg"
      onClick={() => setColorScheme(scheme === 'dark' ? 'light' : 'dark')}
      aria-label="Сменить тему"
    >
      {scheme === 'dark' ? <IconSun size={18} /> : <IconMoon size={18} />}
    </ActionIcon>
  );
}
