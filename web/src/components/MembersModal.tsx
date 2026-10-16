import { useState } from 'react';
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  CopyButton,
  Divider,
  Group,
  Modal,
  Skeleton,
  Stack,
  Text,
  TextInput,
  Tooltip,
} from '@mantine/core';
import { modals } from '@mantine/modals';
import { notifications } from '@mantine/notifications';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  IconBrandTelegram,
  IconCheck,
  IconCopy,
  IconPencil,
  IconRefresh,
  IconUserMinus,
  IconX,
} from '@tabler/icons-react';
import { api, type User } from '../api';
import { formatDate } from '../format';

interface Props {
  opened: boolean;
  onClose: () => void;
  users: User[];
}

/** Mirrors the backend rules (domain.ParseName) to show errors in Russian right away. */
function nameError(name: string): string | null {
  const n = name.trim();
  if (!n) return null; // empty means "use the Telegram name"
  if (n.length > 40) return 'Не длиннее 40 символов';
  if (/\d/.test(n)) return 'Без цифр';
  if (!/^[\p{L} .'-]+$/u.test(n)) return 'Только буквы, пробелы, дефис и точка';
  return null;
}

function MemberRow({ user, onExclude }: { user: User; onExclude: () => void }) {
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(user.custom_name);
  const error = nameError(value);

  const rename = useMutation({
    mutationFn: (name: string) => api.rename(user.telegram_id, name),
    onSuccess: () => {
      setEditing(false);
      // Names are shown in the member list and in the expenses table.
      qc.invalidateQueries({ queryKey: ['users'] });
      qc.invalidateQueries({ queryKey: ['expenses'] });
    },
    onError: (err: Error) => notifications.show({ color: 'red', title: 'Не получилось', message: err.message }),
  });

  const save = () => {
    if (!error) rename.mutate(value.trim());
  };
  const cancel = () => {
    setValue(user.custom_name);
    setEditing(false);
  };

  const telegram = [user.telegram_name && `Telegram: ${user.telegram_name}`, user.username && `@${user.username}`]
    .filter(Boolean)
    .join(' · ');

  return (
    <Group justify="space-between" wrap="nowrap" align="flex-start">
      {editing ? (
        <Group gap={4} wrap="nowrap" align="flex-start" style={{ flex: 1 }}>
          <TextInput
            size="xs"
            value={value}
            onChange={(e) => setValue(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') save();
              if (e.key === 'Escape') cancel();
            }}
            placeholder={user.telegram_name || 'Имя'}
            description="Пусто — имя из Telegram"
            error={error}
            maxLength={40}
            autoFocus
            style={{ flex: 1 }}
          />
          <ActionIcon variant="filled" color="teal" onClick={save} loading={rename.isPending} disabled={!!error} aria-label="Сохранить имя">
            <IconCheck size={16} />
          </ActionIcon>
          <ActionIcon variant="default" onClick={cancel} aria-label="Отменить">
            <IconX size={16} />
          </ActionIcon>
        </Group>
      ) : (
        <div style={{ minWidth: 0 }}>
          <Group gap={4} wrap="nowrap">
            <Text size="sm" fw={500} truncate>
              {user.name}
            </Text>
            <Tooltip label="Переименовать">
              <ActionIcon variant="subtle" color="gray" size="sm" onClick={() => setEditing(true)} aria-label="Переименовать">
                <IconPencil size={14} />
              </ActionIcon>
            </Tooltip>
          </Group>
          <Text size="xs" c="dimmed">
            {[user.custom_name && telegram, user.joined_at && `в семье с ${formatDate(user.joined_at)}`]
              .filter(Boolean)
              .join(' · ') || telegram}
          </Text>
        </div>
      )}
      {!editing && (
        <Button variant="subtle" color="red" size="xs" leftSection={<IconUserMinus size={14} />} onClick={onExclude}>
          Исключить
        </Button>
      )}
    </Group>
  );
}

export function MembersModal({ opened, onClose, users }: Props) {
  const qc = useQueryClient();
  const invite = useQuery({ queryKey: ['invite'], queryFn: api.invite, enabled: opened });

  const fail = (err: Error) => notifications.show({ color: 'red', title: 'Не получилось', message: err.message });

  const rotate = useMutation({
    mutationFn: api.rotateInvite,
    onSuccess: (data) => {
      qc.setQueryData(['invite'], data);
      notifications.show({ color: 'teal', message: 'Ссылка обновлена, старая больше не работает' });
    },
    onError: fail,
  });

  const exclude = useMutation({
    mutationFn: (u: User) => api.exclude(u.telegram_id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['users'] });
      notifications.show({ color: 'teal', message: 'Участник исключён' });
    },
    onError: fail,
  });

  const confirmRotate = () =>
    modals.openConfirmModal({
      title: 'Обновить ссылку?',
      children: (
        <Text size="sm">Старая ссылка перестанет работать. Те, кто уже в семье, останутся в ней.</Text>
      ),
      labels: { confirm: 'Обновить', cancel: 'Отмена' },
      onConfirm: () => rotate.mutate(),
    });

  const confirmExclude = (u: User) =>
    modals.openConfirmModal({
      title: `Исключить ${u.name}?`,
      children: (
        <Text size="sm">
          Бот перестанет отвечать этому человеку. Его траты сохранятся в панели. Если ссылка-приглашение
          осталась у него, обновите её — иначе он сможет вступить снова.
        </Text>
      ),
      labels: { confirm: 'Исключить', cancel: 'Отмена' },
      confirmProps: { color: 'red' },
      onConfirm: () => exclude.mutate(u),
    });

  const members = users.filter((u) => u.is_member);
  const former = users.filter((u) => !u.is_member);

  return (
    <Modal opened={opened} onClose={onClose} title="Участники семьи" size="lg" centered>
      <Stack gap="md">
        <Stack gap={6}>
          <Text fw={600}>Ссылка-приглашение</Text>
          <Text size="sm" c="dimmed">
            Отправьте её членам семьи: открыв ссылку и нажав «Start», человек вступит в семью и сможет вести траты.
          </Text>
          {invite.isLoading && <Skeleton h={36} />}
          {invite.data && !invite.data.link && (
            <Alert color="yellow">
              Telegram-бот не настроен: укажите TELEGRAM_BOT_TOKEN в .env и перезапустите сервис.
            </Alert>
          )}
          {invite.data?.link && (
            <Group gap="xs" wrap="nowrap">
              <TextInput value={invite.data.link} readOnly style={{ flex: 1 }} onFocus={(e) => e.currentTarget.select()} />
              <CopyButton value={invite.data.link} timeout={2000}>
                {({ copied, copy }) => (
                  <Tooltip label={copied ? 'Скопировано' : 'Скопировать'}>
                    <ActionIcon size="lg" variant={copied ? 'filled' : 'default'} color="teal" onClick={copy} aria-label="Скопировать ссылку">
                      {copied ? <IconCheck size={18} /> : <IconCopy size={18} />}
                    </ActionIcon>
                  </Tooltip>
                )}
              </CopyButton>
              <Tooltip label="Открыть в Telegram">
                <ActionIcon
                  size="lg"
                  variant="default"
                  component="a"
                  href={invite.data.link}
                  target="_blank"
                  rel="noreferrer"
                  aria-label="Открыть в Telegram"
                >
                  <IconBrandTelegram size={18} />
                </ActionIcon>
              </Tooltip>
            </Group>
          )}
          {invite.data && (
            <Group>
              <Button
                variant="subtle"
                size="xs"
                leftSection={<IconRefresh size={14} />}
                onClick={confirmRotate}
                loading={rotate.isPending}
              >
                Обновить ссылку
              </Button>
            </Group>
          )}
        </Stack>

        <Divider />

        <Stack gap="xs">
          <Text fw={600}>В семье · {members.length}</Text>
          {!members.length && (
            <Text size="sm" c="dimmed">
              Пока никого. Откройте ссылку сами — так вы тоже станете участником.
            </Text>
          )}
          {members.map((u) => (
            <MemberRow key={u.telegram_id} user={u} onExclude={() => confirmExclude(u)} />
          ))}
        </Stack>

        {former.length > 0 && (
          <Stack gap="xs">
            <Text fw={600} c="dimmed">
              Бывшие участники
            </Text>
            {former.map((u) => (
              <Group key={u.telegram_id} gap="xs">
                <Text size="sm" c="dimmed">
                  {u.name}
                </Text>
                <Badge size="xs" variant="outline" color="gray">
                  исключён
                </Badge>
              </Group>
            ))}
          </Stack>
        )}
      </Stack>
    </Modal>
  );
}
