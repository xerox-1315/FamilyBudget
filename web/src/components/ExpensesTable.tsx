import { ActionIcon, Badge, Group, Paper, Pagination, Skeleton, Stack, Table, Text, Tooltip } from '@mantine/core';
import { IconPencil, IconTrash } from '@tabler/icons-react';
import type { Expense } from '../api';
import { categoryColor, formatDate, formatMoney } from '../format';

interface Props {
  items: Expense[];
  total: number;
  page: number;
  pageSize: number;
  loading: boolean;
  onPage: (page: number) => void;
  onEdit: (e: Expense) => void;
  onDelete: (e: Expense) => void;
}

export function ExpensesTable({ items, total, page, pageSize, loading, onPage, onEdit, onDelete }: Props) {
  const pages = Math.max(1, Math.ceil(total / pageSize));

  if (loading && !items.length) {
    return (
      <Stack gap="xs">
        {Array.from({ length: 5 }, (_, i) => (
          <Skeleton key={i} h={36} />
        ))}
      </Stack>
    );
  }

  if (!items.length) {
    return (
      <Paper withBorder p="xl">
        <Text c="dimmed" ta="center">
          Трат не найдено. Попробуйте изменить фильтры или добавьте трату.
        </Text>
      </Paper>
    );
  }

  return (
    <Stack gap="md">
      <Paper withBorder>
        <Table.ScrollContainer minWidth={640}>
          <Table highlightOnHover verticalSpacing="sm">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Дата</Table.Th>
                <Table.Th>Член семьи</Table.Th>
                <Table.Th>Категория</Table.Th>
                <Table.Th>Комментарий</Table.Th>
                <Table.Th ta="right">Сумма</Table.Th>
                <Table.Th w={90} />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {items.map((e) => (
                <Table.Tr key={e.id}>
                  <Table.Td>{formatDate(e.spent_at)}</Table.Td>
                  <Table.Td>{e.user_name}</Table.Td>
                  <Table.Td>
                    <Badge color={categoryColor(e.category)} variant="light">
                      {e.category_name}
                    </Badge>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" c={e.comment ? undefined : 'dimmed'} lineClamp={2}>
                      {e.comment || '—'}
                    </Text>
                  </Table.Td>
                  <Table.Td ta="right" fw={600} style={{ whiteSpace: 'nowrap' }}>
                    {formatMoney(e.amount)}
                  </Table.Td>
                  <Table.Td>
                    <Group gap={4} justify="flex-end" wrap="nowrap">
                      <Tooltip label="Изменить">
                        <ActionIcon variant="subtle" color="gray" onClick={() => onEdit(e)} aria-label="Изменить">
                          <IconPencil size={18} />
                        </ActionIcon>
                      </Tooltip>
                      <Tooltip label="Удалить">
                        <ActionIcon variant="subtle" color="red" onClick={() => onDelete(e)} aria-label="Удалить">
                          <IconTrash size={18} />
                        </ActionIcon>
                      </Tooltip>
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      </Paper>
      {pages > 1 && (
        <Group justify="center">
          <Pagination value={page} onChange={onPage} total={pages} />
        </Group>
      )}
    </Stack>
  );
}
