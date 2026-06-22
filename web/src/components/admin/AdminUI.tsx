import { ActionIcon, Button, Card, Container, Group, Stack, Text, TextInput, Title } from '@mantine/core'
import { IconSearch } from '@tabler/icons-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

// Page rows shown per admin list.
export const ADMIN_PAGE_LIMIT = 10

/** A consistent admin page shell: title + content. */
export function AdminPageShell({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Container size="lg">
      <Stack gap="lg">
        <Title order={3}>{title}</Title>
        {children}
      </Stack>
    </Container>
  )
}

/** A single labelled statistic card. */
export function StatCard({ label, value }: { label: string; value?: number }) {
  return (
    <Card withBorder padding="md">
      <Text size="xl" fw={700}>
        {value ?? '—'}
      </Text>
      <Text size="sm" c="dimmed">
        {label}
      </Text>
    </Card>
  )
}

/** A right-aligned search field that fires onSubmit on Enter or the icon. */
export function SearchBar({
  value,
  onChange,
  onSubmit,
  placeholder,
}: {
  value: string
  onChange: (v: string) => void
  onSubmit: () => void
  placeholder: string
}) {
  return (
    <Group justify="flex-end">
      <TextInput
        placeholder={placeholder}
        value={value}
        onChange={(e) => onChange(e.currentTarget.value)}
        onKeyDown={(e) => e.key === 'Enter' && onSubmit()}
        rightSection={
          <ActionIcon variant="subtle" onClick={onSubmit}>
            <IconSearch size={16} />
          </ActionIcon>
        }
        w={260}
      />
    </Group>
  )
}

/** Prev/next pager; hidden when everything fits on one page. */
export function Pager({
  page,
  total,
  limit,
  onPrev,
  onNext,
}: {
  page: number
  total: number
  limit: number
  onPrev: () => void
  onNext: () => void
}) {
  const { t } = useTranslation()
  const maxPage = Math.max(1, Math.ceil(total / limit))
  if (total <= limit) return null
  return (
    <Group justify="space-between">
      <Text size="sm" c="dimmed">
        {t('admin.page', { page })} / {maxPage}
      </Text>
      <Group gap="xs">
        <Button size="xs" variant="default" disabled={page <= 1} onClick={onPrev}>
          {t('admin.prev')}
        </Button>
        <Button size="xs" variant="default" disabled={page >= maxPage} onClick={onNext}>
          {t('admin.next')}
        </Button>
      </Group>
    </Group>
  )
}

/** Shared confirm-delete modal target type for users/channels. */
export type AdminDeleteTarget =
  | { kind: 'user'; id: string; label: string }
  | { kind: 'channel'; id: string; label: string }
  | null
