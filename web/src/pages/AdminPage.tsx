import { useEffect, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Card,
  Container,
  Group,
  Menu,
  Modal,
  SimpleGrid,
  Stack,
  Table,
  Tabs,
  Text,
  TextInput,
  Title,
} from '@mantine/core'
import { IconDots, IconSearch } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { Trans, useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useAuth } from '../auth/context'
import type { AdminChannel, AdminStats, AdminUser, Paged } from '../lib/types'

const LIMIT = 10

type DeleteTarget =
  | { kind: 'user'; id: string; label: string }
  | { kind: 'channel'; id: string; label: string }
  | null

export function AdminPage() {
  const { t } = useTranslation()
  const { userId } = useAuth()
  const navigate = useNavigate()

  const [stats, setStats] = useState<AdminStats | null>(null)

  const [userQuery, setUserQuery] = useState('')
  const [userSearch, setUserSearch] = useState('')
  const [userPage, setUserPage] = useState(1)
  const [users, setUsers] = useState<Paged<AdminUser> | null>(null)

  const [channelQuery, setChannelQuery] = useState('')
  const [channelSearch, setChannelSearch] = useState('')
  const [channelPage, setChannelPage] = useState(1)
  const [channels, setChannels] = useState<Paged<AdminChannel> | null>(null)

  const [target, setTarget] = useState<DeleteTarget>(null)
  const [deleting, setDeleting] = useState(false)

  function notifyError(key: string, e: unknown) {
    notifications.show({ color: 'red', message: `${t(key)}: ${String(e)}` })
  }

  useEffect(() => {
    let active = true
    api.adminStats().then((s) => active && setStats(s)).catch((e) => notifyError('admin.loadFailed', e))
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  function reloadUsers() {
    api
      .adminListUsers(userQuery, userPage, LIMIT)
      .then(setUsers)
      .catch((e) => notifyError('admin.loadFailed', e))
  }

  function reloadChannels() {
    api
      .adminListChannels(channelQuery, channelPage, LIMIT)
      .then(setChannels)
      .catch((e) => notifyError('admin.loadFailed', e))
  }

  useEffect(() => {
    let active = true
    api
      .adminListUsers(userQuery, userPage, LIMIT)
      .then((p) => active && setUsers(p))
      .catch((e) => notifyError('admin.loadFailed', e))
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userQuery, userPage])

  useEffect(() => {
    let active = true
    api
      .adminListChannels(channelQuery, channelPage, LIMIT)
      .then((p) => active && setChannels(p))
      .catch((e) => notifyError('admin.loadFailed', e))
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelQuery, channelPage])

  async function updateUser(id: string, body: { role?: 'user' | 'admin'; status?: 'active' | 'blocked' }) {
    try {
      await api.adminUpdateUser(id, body)
      notifications.show({ color: 'green', message: t('admin.userUpdated') })
      reloadUsers()
    } catch (e) {
      notifyError('admin.updateFailed', e)
    }
  }

  async function setChannelStatus(id: string, status: 'active' | 'disabled') {
    try {
      await api.adminUpdateChannelStatus(id, status)
      notifications.show({ color: 'green', message: t('admin.channelUpdated') })
      reloadChannels()
    } catch (e) {
      notifyError('admin.updateFailed', e)
    }
  }

  async function confirmDelete() {
    if (!target) return
    setDeleting(true)
    try {
      if (target.kind === 'user') {
        await api.adminDeleteUser(target.id)
        notifications.show({ color: 'green', message: t('admin.userDeleted') })
        reloadUsers()
      } else {
        await api.adminDeleteChannel(target.id)
        notifications.show({ color: 'green', message: t('admin.channelDeleted') })
        reloadChannels()
      }
      setTarget(null)
    } catch (e) {
      notifyError('admin.deleteFailed', e)
    } finally {
      setDeleting(false)
    }
  }

  return (
    <Container size="lg">
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
            <Stack gap="sm">
              <SearchBar
                value={userSearch}
                onChange={setUserSearch}
                onSubmit={() => {
                  setUserPage(1)
                  setUserQuery(userSearch.trim())
                }}
                placeholder={t('admin.searchUsers')}
              />
              {users && users.items.length === 0 ? (
                <Text c="dimmed" size="sm">
                  {t('admin.noUsers')}
                </Text>
              ) : (
                <Table verticalSpacing="xs" striped>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t('admin.colEmail')}</Table.Th>
                      <Table.Th>{t('admin.colRole')}</Table.Th>
                      <Table.Th>{t('admin.colStatus')}</Table.Th>
                      <Table.Th>{t('admin.colChannels')}</Table.Th>
                      <Table.Th />
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {users?.items.map((u) => (
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
                        <Table.Td>
                          <Badge color={u.status === 'blocked' ? 'red' : 'teal'} variant="light">
                            {u.status === 'blocked' ? t('admin.statusBlocked') : t('admin.statusActive')}
                          </Badge>
                        </Table.Td>
                        <Table.Td>{u.channelCount}</Table.Td>
                        <Table.Td align="right">
                          {u.id !== userId && (
                            <Menu position="bottom-end" withinPortal>
                              <Menu.Target>
                                <ActionIcon variant="subtle" color="gray">
                                  <IconDots size={16} />
                                </ActionIcon>
                              </Menu.Target>
                              <Menu.Dropdown>
                                {u.role === 'admin' ? (
                                  <Menu.Item onClick={() => updateUser(u.id, { role: 'user' })}>
                                    {t('admin.makeUser')}
                                  </Menu.Item>
                                ) : (
                                  <Menu.Item onClick={() => updateUser(u.id, { role: 'admin' })}>
                                    {t('admin.makeAdmin')}
                                  </Menu.Item>
                                )}
                                {u.status === 'blocked' ? (
                                  <Menu.Item onClick={() => updateUser(u.id, { status: 'active' })}>
                                    {t('admin.unblock')}
                                  </Menu.Item>
                                ) : (
                                  <Menu.Item onClick={() => updateUser(u.id, { status: 'blocked' })}>
                                    {t('admin.block')}
                                  </Menu.Item>
                                )}
                                <Menu.Divider />
                                <Menu.Item
                                  color="red"
                                  onClick={() => setTarget({ kind: 'user', id: u.id, label: u.email })}
                                >
                                  {t('common.delete')}
                                </Menu.Item>
                              </Menu.Dropdown>
                            </Menu>
                          )}
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              )}
              <Pager
                page={users?.page ?? 1}
                total={users?.total ?? 0}
                limit={LIMIT}
                onPrev={() => setUserPage((p) => Math.max(1, p - 1))}
                onNext={() => setUserPage((p) => p + 1)}
              />
            </Stack>
          </Tabs.Panel>

          <Tabs.Panel value="channels">
            <Stack gap="sm">
              <SearchBar
                value={channelSearch}
                onChange={setChannelSearch}
                onSubmit={() => {
                  setChannelPage(1)
                  setChannelQuery(channelSearch.trim())
                }}
                placeholder={t('admin.searchChannels')}
              />
              {channels && channels.items.length === 0 ? (
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
                      <Table.Th>{t('admin.colStatus')}</Table.Th>
                      <Table.Th />
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {channels?.items.map((c) => (
                      <Table.Tr key={c.id}>
                        <Table.Td>
                          <Text
                            style={{ cursor: 'pointer' }}
                            fw={500}
                            onClick={() => navigate(`/admin/channels/${c.id}`)}
                          >
                            {c.name}
                          </Text>
                        </Table.Td>
                        <Table.Td>{c.ownerEmail}</Table.Td>
                        <Table.Td>
                          <Badge color={c.subscriptionMode === 'open' ? 'teal' : 'grape'}>
                            {t(`mode.${c.subscriptionMode}`)}
                          </Badge>
                        </Table.Td>
                        <Table.Td>
                          <Badge color={c.status === 'disabled' ? 'red' : 'teal'} variant="light">
                            {c.status === 'disabled' ? t('admin.statusDisabled') : t('admin.statusActive')}
                          </Badge>
                        </Table.Td>
                        <Table.Td align="right">
                          <Menu position="bottom-end" withinPortal>
                            <Menu.Target>
                              <ActionIcon variant="subtle" color="gray">
                                <IconDots size={16} />
                              </ActionIcon>
                            </Menu.Target>
                            <Menu.Dropdown>
                              <Menu.Item onClick={() => navigate(`/admin/channels/${c.id}`)}>
                                {t('admin.viewMessages')}
                              </Menu.Item>
                              {c.status === 'disabled' ? (
                                <Menu.Item onClick={() => setChannelStatus(c.id, 'active')}>
                                  {t('admin.enable')}
                                </Menu.Item>
                              ) : (
                                <Menu.Item onClick={() => setChannelStatus(c.id, 'disabled')}>
                                  {t('admin.disable')}
                                </Menu.Item>
                              )}
                              <Menu.Divider />
                              <Menu.Item
                                color="red"
                                onClick={() => setTarget({ kind: 'channel', id: c.id, label: c.name })}
                              >
                                {t('common.delete')}
                              </Menu.Item>
                            </Menu.Dropdown>
                          </Menu>
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              )}
              <Pager
                page={channels?.page ?? 1}
                total={channels?.total ?? 0}
                limit={LIMIT}
                onPrev={() => setChannelPage((p) => Math.max(1, p - 1))}
                onNext={() => setChannelPage((p) => p + 1)}
              />
            </Stack>
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

function SearchBar({
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

function Pager({
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
