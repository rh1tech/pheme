import { useEffect, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Group,
  Menu,
  PasswordInput,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
} from '@mantine/core'
import { IconDots, IconUserPlus } from '@tabler/icons-react'
import { Trans, useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { notifyError, notifySuccess } from '../../lib/notify'
import { checkPassword } from '../../lib/password'
import { useAuth } from '../../auth/context'
import type { AdminUser, Paged, Role, UserStatus } from '../../lib/types'
import { ADMIN_PAGE_LIMIT, AdminPageShell, Pager, SearchBar } from '../../components/admin/AdminUI'
import { ConfirmModal } from '../../components/ConfirmModal'
import { ResponsiveModal } from '../../components/ResponsiveModal'
import { PasswordStrength } from '../../components/PasswordStrength'
import { RoleBadge, UserStatusBadge } from '../../components/badges'
import { TableRowsSkeleton } from '../../components/Skeletons'

export function AdminUsersPage() {
  const { t } = useTranslation()
  const { userId } = useAuth()
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(1)
  const [users, setUsers] = useState<Paged<AdminUser> | null>(null)
  const [deleteId, setDeleteId] = useState<{ id: string; email: string } | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [resetUser, setResetUser] = useState<{ id: string; email: string } | null>(null)
  const [newPassword, setNewPassword] = useState('')
  const [resetting, setResetting] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [createEmail, setCreateEmail] = useState('')
  const [createPassword, setCreatePassword] = useState('')
  const [createRole, setCreateRole] = useState<Role>('user')
  const [creating, setCreating] = useState(false)

  function reload() {
    api
      .adminListUsers(query, page, ADMIN_PAGE_LIMIT)
      .then(setUsers)
      .catch((e) => notifyError(t('admin.loadFailed'), e))
  }

  useEffect(() => {
    let active = true
    api
      .adminListUsers(query, page, ADMIN_PAGE_LIMIT)
      .then((p) => active && setUsers(p))
      .catch((e) => active && notifyError(t('admin.loadFailed'), e))
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, page])

  async function update(id: string, body: { role?: Role; status?: UserStatus }) {
    try {
      await api.adminUpdateUser(id, body)
      notifySuccess(t('admin.userUpdated'))
      reload()
    } catch (e) {
      notifyError(t('admin.updateFailed'), e)
    }
  }

  function openCreate() {
    setCreateEmail('')
    setCreatePassword('')
    setCreateRole('user')
    setCreateOpen(true)
  }

  const canCreate = createEmail.trim() !== '' && checkPassword(createPassword).acceptable

  async function confirmCreate() {
    if (!canCreate) return
    setCreating(true)
    try {
      await api.adminCreateUser({
        email: createEmail.trim(),
        password: createPassword,
        role: createRole,
      })
      notifySuccess(t('admin.userCreated'))
      setCreateOpen(false)
      setPage(1)
      reload()
    } catch (e) {
      notifyError(t('admin.createUserFailed'), e)
    } finally {
      setCreating(false)
    }
  }

  function openReset(user: { id: string; email: string }) {
    setNewPassword('')
    setResetUser(user)
  }

  async function confirmReset() {
    if (!resetUser || !checkPassword(newPassword).acceptable) return
    setResetting(true)
    try {
      await api.adminResetUserPassword(resetUser.id, newPassword)
      notifySuccess(t('admin.passwordReset'))
      setResetUser(null)
    } catch (e) {
      notifyError(t('admin.resetPasswordFailed'), e)
    } finally {
      setResetting(false)
    }
  }

  async function confirmDelete() {
    if (!deleteId) return
    setDeleting(true)
    try {
      await api.adminDeleteUser(deleteId.id)
      notifySuccess(t('admin.userDeleted'))
      setDeleteId(null)
      reload()
    } catch (e) {
      notifyError(t('admin.deleteFailed'), e)
    } finally {
      setDeleting(false)
    }
  }

  return (
    <AdminPageShell
      title={t('admin.headerUsers')}
      actions={
        <Group gap="sm" wrap="wrap">
          <SearchBar
            value={search}
            onChange={setSearch}
            onSubmit={() => {
              setPage(1)
              setQuery(search.trim())
            }}
            placeholder={t('admin.searchUsers')}
          />
          <Button leftSection={<IconUserPlus size={16} />} onClick={openCreate}>
            {t('admin.addUser')}
          </Button>
        </Group>
      }
    >
      {/* A sheet on mobile, anchored above the keyboard — this dialog is all text fields. */}
      <ResponsiveModal opened={createOpen} onClose={() => setCreateOpen(false)} title={t('admin.addUser')}>
        <Stack gap="sm">
          <TextInput
            label={t('auth.email')}
            type="email"
            data-autofocus
            value={createEmail}
            onChange={(e) => setCreateEmail(e.currentTarget.value)}
          />
          <div>
            <PasswordInput
              label={t('auth.password')}
              value={createPassword}
              onChange={(e) => setCreatePassword(e.currentTarget.value)}
            />
            <PasswordStrength value={createPassword} />
          </div>
          <Select
            label={t('admin.colRole')}
            value={createRole}
            onChange={(v) => setCreateRole((v as Role) ?? 'user')}
            allowDeselect={false}
            data={[
              { value: 'user', label: t('admin.roleUser') },
              { value: 'admin', label: t('admin.roleAdmin') },
            ]}
          />
          <Group justify="flex-end" mt="sm">
            <Button variant="default" onClick={() => setCreateOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button onClick={confirmCreate} loading={creating} disabled={!canCreate}>
              {t('admin.addUser')}
            </Button>
          </Group>
        </Stack>
      </ResponsiveModal>

      <ConfirmModal
        opened={deleteId !== null}
        onClose={() => setDeleteId(null)}
        onConfirm={confirmDelete}
        title={t('common.delete')}
        loading={deleting}
      >
        <Text size="sm">
          <Trans i18nKey="admin.deleteUserConfirm" values={{ email: deleteId?.email ?? '' }} components={{ bold: <b /> }} />
        </Text>
      </ConfirmModal>

      <ConfirmModal
        opened={resetUser !== null}
        onClose={() => setResetUser(null)}
        onConfirm={confirmReset}
        title={t('admin.resetPassword')}
        confirmLabel={t('admin.resetPassword')}
        confirmColor="iris"
        loading={resetting}
        confirmDisabled={!checkPassword(newPassword).acceptable}
      >
        <Text size="sm">
          <Trans i18nKey="admin.resetPasswordConfirm" values={{ email: resetUser?.email ?? '' }} components={{ bold: <b /> }} />
        </Text>
        <PasswordInput
          label={t('auth.newPassword')}
          value={newPassword}
          onChange={(e) => setNewPassword(e.currentTarget.value)}
          mt="sm"
        />
        <PasswordStrength value={newPassword} />
      </ConfirmModal>

      {users && users.items.length === 0 ? (
        <Text c="dimmed" size="sm">
          {t('admin.noUsers')}
        </Text>
      ) : (
        <Table.ScrollContainer minWidth={640}>
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
          {users === null ? (
            <TableRowsSkeleton rows={ADMIN_PAGE_LIMIT} cols={5} />
          ) : (
            <Table.Tbody>
              {users.items.map((u) => (
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
                  <RoleBadge role={u.role} />
                </Table.Td>
                <Table.Td>
                  <UserStatusBadge status={u.status} />
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
                          <Menu.Item onClick={() => update(u.id, { role: 'user' })}>{t('admin.makeUser')}</Menu.Item>
                        ) : (
                          <Menu.Item onClick={() => update(u.id, { role: 'admin' })}>{t('admin.makeAdmin')}</Menu.Item>
                        )}
                        {u.status === 'blocked' ? (
                          <Menu.Item onClick={() => update(u.id, { status: 'active' })}>{t('admin.unblock')}</Menu.Item>
                        ) : (
                          <Menu.Item onClick={() => update(u.id, { status: 'blocked' })}>{t('admin.block')}</Menu.Item>
                        )}
                        <Menu.Item onClick={() => openReset({ id: u.id, email: u.email })}>
                          {t('admin.resetPassword')}
                        </Menu.Item>
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
          )}
          </Table>
        </Table.ScrollContainer>
      )}
      <Pager
        page={users?.page ?? 1}
        total={users?.total ?? 0}
        limit={ADMIN_PAGE_LIMIT}
        onPrev={() => setPage((p) => Math.max(1, p - 1))}
        onNext={() => setPage((p) => p + 1)}
      />
    </AdminPageShell>
  )
}
