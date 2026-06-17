import { useEffect, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Card,
  Container,
  Group,
  Modal,
  SimpleGrid,
  Stack,
  Table,
  Tabs,
  Text,
  Title,
  Tooltip,
} from '@mantine/core'
import { IconTrash } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { Trans, useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { useAuth } from '../auth/context'
import type { AdminChannel, AdminStats, AdminUser } from '../lib/types'

type DeleteTarget =
  | { kind: 'user'; id: string; label: string }
  | { kind: 'channel'; id: string; label: string }
  | null

export function AdminPage() {
  const { t } = useTranslation()
  const { userId } = useAuth()
  const [stats, setStats] = useState<AdminStats | null>(null)
  const [users, setUsers] = useState<AdminUser[]>([])
  const [channels, setChannels] = useState<AdminChannel[]>([])
  const [target, setTarget] = useState<DeleteTarget>(null)
  const [deleting, setDeleting] = useState(false)

  function loadAll() {
    api.adminStats().then(setStats).catch(notifyLoadError)
    api.adminListUsers().then(setUsers).catch(notifyLoadError)
    api.adminListChannels().then(setChannels).catch(notifyLoadError)
  }

  function notifyLoadError(e: unknown) {
    notifications.show({ color: 'red', message: `${t('admin.loadFailed')}: ${String(e)}` })
  }

  useEffect(() => {
    let active = true
    api.adminStats().then((s) => active && setStats(s)).catch(notifyLoadError)
    api.adminListUsers().then((u) => active && setUsers(u)).catch(notifyLoadError)
    api.adminListChannels().then((c) => active && setChannels(c)).catch(notifyLoadError)
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function confirmDelete() {
    if (!target) return
    setDeleting(true)
    try {
      if (target.kind === 'user') {
        await api.adminDeleteUser(target.id)
        notifications.show({ color: 'green', message: t('admin.userDeleted') })
      } else {
        await api.adminDeleteChannel(target.id)
        notifications.show({ color: 'green', message: t('admin.channelDeleted') })
      }
      setTarget(null)
      loadAll()
    } catch (e) {
      notifications.show({ color: 'red', message: `${t('admin.deleteFailed')}: ${String(e)}` })
    } finally {
      setDeleting(false)
    }
  }

  return (
    <Container size="md">
      <Modal opened={target !== null} onClose={() => setTarget(null)} title={t('common.delete')}>
        <Stack>
          <Text size="sm">
            <Trans
              i18nKey={target?.kind === 'user' ? 'admin.deleteUserConfirm' : 'admin.deleteChannelConfirm'}
              values={target?.kind === 'user' ? { email: target.label } : { name: target?.label ?? '' }}
              components={{ bold: <b /> }}
            />
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button color="red" loading={deleting} onClick={confirmDelete}>
              {t('common.delete')}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Stack gap="lg">
        <Title order={3}>{t('admin.title')}</Title>

        <Tabs defaultValue="overview" keepMounted={false}>
          <Tabs.List mb="md">
            <Tabs.Tab value="overview">{t('admin.tabOverview')}</Tabs.Tab>
            <Tabs.Tab value="users">{t('admin.tabUsers')}</Tabs.Tab>
            <Tabs.Tab value="channels">{t('admin.tabChannels')}</Tabs.Tab>
          </Tabs.List>

          <Tabs.Panel value="overview">
            <Stack gap="lg">
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
            </Stack>
          </Tabs.Panel>

          <Tabs.Panel value="users">
            {users.length === 0 ? (
              <Text c="dimmed" size="sm">
                {t('admin.noUsers')}
              </Text>
            ) : (
              <Table verticalSpacing="xs" striped>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t('admin.colEmail')}</Table.Th>
                    <Table.Th>{t('admin.colRole')}</Table.Th>
                    <Table.Th>{t('admin.colChannels')}</Table.Th>
                    <Table.Th>{t('admin.colCreated')}</Table.Th>
                    <Table.Th />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {users.map((u) => (
                    <Table.Tr key={u.id}>
                      <Table.Td>
                        <Group gap="xs">
                          {u.email}
                          {u.id === userId && (
                            <Badge size="xs" variant="outline">
                              {t('admin.you')}
                            </Badge>
                          )}
                        </Group>
                      </Table.Td>
                      <Table.Td>
                        <Badge color={u.role === 'admin' ? 'grape' : 'gray'}>
                          {u.role === 'admin' ? t('admin.roleAdmin') : t('admin.roleUser')}
                        </Badge>
                      </Table.Td>
                      <Table.Td>{u.channelCount}</Table.Td>
                      <Table.Td>{new Date(u.createdAt).toLocaleDateString()}</Table.Td>
                      <Table.Td align="right">
                        {u.id !== userId && (
                          <Tooltip label={t('common.delete')}>
                            <ActionIcon
                              color="red"
                              variant="subtle"
                              onClick={() => setTarget({ kind: 'user', id: u.id, label: u.email })}
                            >
                              <IconTrash size={16} />
                            </ActionIcon>
                          </Tooltip>
                        )}
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            )}
          </Tabs.Panel>

          <Tabs.Panel value="channels">
            {channels.length === 0 ? (
              <Text c="dimmed" size="sm">
                {t('admin.noChannels')}
              </Text>
            ) : (
              <Table verticalSpacing="xs" striped>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t('admin.colName')}</Table.Th>
                    <Table.Th>{t('admin.colOwner')}</Table.Th>
                    <Table.Th>{t('admin.colMode')}</Table.Th>
                    <Table.Th>{t('admin.colCreated')}</Table.Th>
                    <Table.Th />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {channels.map((c) => (
                    <Table.Tr key={c.id}>
                      <Table.Td>{c.name}</Table.Td>
                      <Table.Td>{c.ownerEmail}</Table.Td>
                      <Table.Td>
                        <Badge color={c.subscriptionMode === 'open' ? 'teal' : 'grape'}>
                          {t(`mode.${c.subscriptionMode}`)}
                        </Badge>
                      </Table.Td>
                      <Table.Td>{new Date(c.createdAt).toLocaleDateString()}</Table.Td>
                      <Table.Td align="right">
                        <Tooltip label={t('common.delete')}>
                          <ActionIcon
                            color="red"
                            variant="subtle"
                            onClick={() => setTarget({ kind: 'channel', id: c.id, label: c.name })}
                          >
                            <IconTrash size={16} />
                          </ActionIcon>
                        </Tooltip>
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            )}
          </Tabs.Panel>
        </Tabs>
      </Stack>
    </Container>
  )
}

function StatCard({ label, value }: { label: string; value?: number }) {
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
