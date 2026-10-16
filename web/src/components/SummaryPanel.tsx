import { Badge, Group, Paper, SimpleGrid, Skeleton, Stack, Text, Title } from '@mantine/core';
import { DonutChart } from '@mantine/charts';
import type { Summary } from '../api';
import { categoryColors, formatMoney } from '../format';

interface Props {
  summary?: Summary;
  loading: boolean;
}

export function SummaryPanel({ summary, loading }: Props) {
  if (loading && !summary) {
    return <Skeleton h={220} radius="md" />;
  }
  const s = summary ?? { total_amount: 0, count: 0, by_category: [] };
  const data = s.by_category.map((c) => ({
    name: c.name,
    value: c.amount / 100,
    color: categoryColors[c.category],
  }));

  return (
    <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
      <Paper withBorder p="lg">
        <Stack gap={4}>
          <Text c="dimmed" size="sm">
            Потрачено за период
          </Text>
          <Title order={2}>{formatMoney(s.total_amount)}</Title>
          <Text c="dimmed" size="sm">
            {s.count} {plural(s.count, 'трата', 'траты', 'трат')}
          </Text>
        </Stack>
        <Stack gap={6} mt="md">
          {s.by_category.map((c) => (
            <Group key={c.category} justify="space-between" wrap="nowrap">
              <Badge color={categoryColors[c.category].split('.')[0]} variant="light">
                {c.name}
              </Badge>
              <Text size="sm" fw={500}>
                {formatMoney(c.amount)}
                <Text span c="dimmed" size="xs" ml={6}>
                  {s.total_amount ? Math.round((c.amount / s.total_amount) * 100) : 0}%
                </Text>
              </Text>
            </Group>
          ))}
        </Stack>
      </Paper>
      <Paper withBorder p="lg">
        <Text c="dimmed" size="sm" mb="sm">
          По категориям
        </Text>
        {data.length ? (
          <Group justify="center">
            <DonutChart
              data={data}
              size={200}
              thickness={28}
              withTooltip
              tooltipDataSource="segment"
              valueFormatter={(v) => formatMoney(Math.round(v * 100))}
            />
          </Group>
        ) : (
          <Text c="dimmed" ta="center" py="xl">
            Нет трат за выбранный период
          </Text>
        )}
      </Paper>
    </SimpleGrid>
  );
}

export function plural(n: number, one: string, few: string, many: string) {
  const m10 = n % 10;
  const m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 10 || m100 >= 20)) return few;
  return many;
}
