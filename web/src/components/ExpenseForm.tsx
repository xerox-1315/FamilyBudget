import { useState } from 'react';
import { Button, Group, NumberInput, Select, Stack, TextInput } from '@mantine/core';
import { DatePickerInput } from '@mantine/dates';
import dayjs from 'dayjs';
import type { Category, CategoryCode, Expense, ExpenseInput, User } from '../api';
import { rublesToKopecks } from '../format';

interface Props {
  expense?: Expense;
  users: User[];
  categories: Category[];
  submitting: boolean;
  onSubmit: (input: ExpenseInput) => void;
  onCancel: () => void;
}

export function ExpenseForm({ expense, users, categories, submitting, onSubmit, onCancel }: Props) {
  const [userId, setUserId] = useState<string | null>(
    expense ? String(expense.user_id) : users.length === 1 ? String(users[0].telegram_id) : null,
  );
  const [amount, setAmount] = useState<number | string>(expense ? expense.amount / 100 : '');
  const [category, setCategory] = useState<string | null>(expense?.category ?? null);
  const [date, setDate] = useState<string | null>(expense?.spent_at ?? dayjs().format('YYYY-MM-DD'));
  const [comment, setComment] = useState(expense?.comment ?? '');
  const [touched, setTouched] = useState(false);

  const kopecks = amount === '' ? 0 : rublesToKopecks(amount);
  const errors = {
    user: !userId ? 'Выберите члена семьи' : null,
    amount: kopecks <= 0 ? 'Введите сумму больше нуля' : null,
    category: !category ? 'Выберите категорию' : null,
    date: !date ? 'Укажите дату' : null,
  };
  const valid = Object.values(errors).every((e) => !e);
  const show = (e: string | null) => (touched ? e : null);

  const submit = (ev: React.FormEvent) => {
    ev.preventDefault();
    setTouched(true);
    if (!valid) return;
    onSubmit({
      user_id: Number(userId),
      amount: kopecks,
      category: category as CategoryCode,
      spent_at: date!,
      comment: comment.trim(),
    });
  };

  return (
    <form onSubmit={submit} noValidate>
      <Stack gap="sm">
        <Select
          label="Член семьи"
          data={users.map((u) => ({ value: String(u.telegram_id), label: u.name }))}
          value={userId}
          onChange={setUserId}
          disabled={!!expense}
          description={expense ? 'Владельца траты изменить нельзя' : undefined}
          error={show(errors.user)}
          searchable
          required
        />
        <NumberInput
          label="Сумма, ₽"
          value={amount}
          onChange={setAmount}
          min={0}
          decimalScale={2}
          decimalSeparator=","
          thousandSeparator=" "
          hideControls
          error={show(errors.amount)}
          required
          data-autofocus
        />
        <Select
          label="Категория"
          data={categories.map((c) => ({ value: c.code, label: c.name }))}
          value={category}
          onChange={setCategory}
          error={show(errors.category)}
          required
        />
        <DatePickerInput
          label="Дата"
          value={date}
          onChange={setDate}
          valueFormat="DD.MM.YYYY"
          maxDate={dayjs().add(1, 'day').format('YYYY-MM-DD')}
          error={show(errors.date)}
          required
        />
        <TextInput
          label="Комментарий"
          placeholder="Например, кофе"
          value={comment}
          onChange={(e) => setComment(e.currentTarget.value)}
          maxLength={500}
        />
        <Group justify="flex-end" mt="sm">
          <Button variant="default" onClick={onCancel}>
            Отмена
          </Button>
          <Button type="submit" loading={submitting}>
            {expense ? 'Сохранить' : 'Добавить'}
          </Button>
        </Group>
      </Stack>
    </form>
  );
}
