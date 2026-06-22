import { useEffect, useState } from 'react'
import { Badge, Card, Group, SimpleGrid, Stack, Text, Title } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import type { AdminStats } from '../../lib/types'
import { AdminPageShell, StatCard } from '../../components/admin/AdminUI'

export function AdminOverviewPage() {
  const { t } = useTranslation()
  const [stats, setStats] = useState<AdminStats | null>(null)

  useEffect(() => {
    let active = true
    api
      .adminStats()
      .then((s) => active && setStats(s))
      .catch((e) => active && notifications.show({ color: 'red', message: `${t('admin.loadFailed')}: ${String(e)}` }))
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <AdminPageShell title={t('admin.headerOverview')}>
      <SimpleGrid cols={{ base: 2, sm: 5 }}>
        <StatCard label={t('admin.statUsers')} value={stats?.users} />
        <StatCard label={t('admin.statChannels')} value={stats?.channels} />
        <StatCard label={t('admin.statMessages')} value={stats?.messages} />
        <StatCard label={t('admin.statDeliveries')} value={stats?.deliveries} />
        <StatCard label={t('admin.statDevices')} value={stats?.devices} />
      </SimpleGrid>

      <Card withBorder padding="lg">
        <Title order={5} mb="sm">
          {t('admin.topChannels')}
        </Title>
        {!stats?.topChannels?.length ? (
          <Text c="dimmed" size="sm">
            {t('admin.noTopChannels')}
          </Text>
        ) : (
          <Stack gap="xs">
            {stats.topChannels.map((c) => (
              <Group key={c.channelId} justify="space-between">
                <Text>{c.name}</Text>
                <Badge variant="light">{t('admin.messagesCount', { count: c.count })}</Badge>
              </Group>
            ))}
          </Stack>
        )}
      </Card>

      <Card withBorder padding="lg">
        <Title order={5} mb="sm">
          {t('admin.recentMessages')}
        </Title>
        {!stats?.recentMessages?.length ? (
          <Text c="dimmed" size="sm">
            {t('admin.noRecentMessages')}
          </Text>
        ) : (
          <Stack gap="xs">
            {stats.recentMessages.map((m) => (
              <Group key={m.id} justify="space-between" wrap="nowrap">
                <Text truncate>{m.title || m.body || '—'}</Text>
                <Text size="xs" c="dimmed">
                  {new Date(m.createdAt).toLocaleString()}
                </Text>
              </Group>
            ))}
          </Stack>
        )}
      </Card>
    </AdminPageShell>
  )
}
