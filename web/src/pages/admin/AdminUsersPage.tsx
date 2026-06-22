import { useEffect, useState } from 'react'
import { ActionIcon, Badge, Button, Group, Menu, Modal, Stack, Table, Text } from '@mantine/core'
import { IconDots } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { Trans, useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { useAuth } from '../../auth/context'
import type { AdminUser, Paged, Role, UserStatus } from '../../lib/types'
import { ADMIN_PAGE_LIMIT, AdminPageShell, Pager, SearchBar } from '../../components/admin/AdminUI'

export function AdminUsersPage() {
  const { t } = useTranslation()
  const { userId } = useAuth()
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(1)
  const [users, setUsers] = useState<Paged<AdminUser> | null>(null)
  const [deleteId, setDeleteId] = useState<{ id: string; email: string } | null>(null)
  const [deleting, setDeleting] = useState(false)

  function notifyError(key: string, e: unknown) {
    notifications.show({ color: 'red', message: `${t(key)}: ${String(e)}` })
  }

  function reload() {
    api
      .adminListUsers(query, page, ADMIN_PAGE_LIMIT)
      .then(setUsers)
      .catch((e) => notifyError('admin.loadFailed', e))
  }

  useEffect(() => {
    let active = true
    api
      .adminListUsers(query, page, ADMIN_PAGE_LIMIT)
      .then((p) => active && setUsers(p))
      .catch((e) => active && notifyError('admin.loadFailed', e))
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, page])

  async function update(id: string, body: { role?: Role; status?: UserStatus }) {
    try {
      await api.adminUpdateUser(id, body)
      notifications.show({ color: 'green', message: t('admin.userUpdated') })
      reload()
    } catch (e) {
      notifyError('admin.updateFailed', e)
    }
  }

  async function confirmDelete() {
    if (!deleteId) return
    setDeleting(true)
    try {
      await api.adminDeleteUser(deleteId.id)
      notifications.show({ color: 'green', message: t('admin.userDeleted') })
      setDeleteId(null)
      reload()
    } catch (e) {
      notifyError('admin.deleteFailed', e)
    } finally {
      setDeleting(false)
    }
  }

  return (
    <AdminPageShell title={t('admin.headerUsers')}>
      <Modal opened={deleteId !== null} onClose={() => setDeleteId(null)} title={t('common.delete')}>
        <Stack>
          <Text size="sm">
            <Trans
              i18nKey="admin.deleteUserConfirm"
              values={{ email: deleteId?.email ?? '' }}
              components={{ bold: <b /> }}
            />
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setDeleteId(null)}>
              {t('common.cancel')}
            </Button>
            <Button color="red" loading={deleting} onClick={confirmDelete}>
              {t('common.delete')}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Stack gap="sm">
        <SearchBar
          value={search}
          onChange={setSearch}
          onSubmit={() => {
            setPage(1)
            setQuery(search.trim())
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
                            <Menu.Item onClick={() => update(u.id, { role: 'user' })}>
                              {t('admin.makeUser')}
                            </Menu.Item>
                          ) : (
                            <Menu.Item onClick={() => update(u.id, { role: 'admin' })}>
                              {t('admin.makeAdmin')}
                            </Menu.Item>
                          )}
                          {u.status === 'blocked' ? (
                            <Menu.Item onClick={() => update(u.id, { status: 'active' })}>
                              {t('admin.unblock')}
                            </Menu.Item>
                          ) : (
                            <Menu.Item onClick={() => update(u.id, { status: 'blocked' })}>
                              {t('admin.block')}
                            </Menu.Item>
                          )}
                          <Menu.Divider />
                          <Menu.Item color="red" onClick={() => setDeleteId({ id: u.id, email: u.email })}>
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
          limit={ADMIN_PAGE_LIMIT}
          onPrev={() => setPage((p) => Math.max(1, p - 1))}
          onNext={() => setPage((p) => p + 1)}
        />
      </Stack>
    </AdminPageShell>
  )
}
