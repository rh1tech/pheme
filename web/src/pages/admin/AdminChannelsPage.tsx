import { useEffect, useState } from 'react'
import { ActionIcon, Badge, Button, Group, Menu, Modal, Stack, Table, Text } from '@mantine/core'
import { IconDots } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { Trans, useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { api } from '../../lib/api'
import type { AdminChannel, ChannelStatus, Paged } from '../../lib/types'
import { ADMIN_PAGE_LIMIT, AdminPageShell, Pager, SearchBar } from '../../components/admin/AdminUI'

export function AdminChannelsPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(1)
  const [channels, setChannels] = useState<Paged<AdminChannel> | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; name: string } | null>(null)
  const [deleting, setDeleting] = useState(false)

  function notifyError(key: string, e: unknown) {
    notifications.show({ color: 'red', message: `${t(key)}: ${String(e)}` })
  }

  function reload() {
    api
      .adminListChannels(query, page, ADMIN_PAGE_LIMIT)
      .then(setChannels)
      .catch((e) => notifyError('admin.loadFailed', e))
  }

  useEffect(() => {
    let active = true
    api
      .adminListChannels(query, page, ADMIN_PAGE_LIMIT)
      .then((p) => active && setChannels(p))
      .catch((e) => active && notifyError('admin.loadFailed', e))
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, page])

  async function setStatus(id: string, status: ChannelStatus) {
    try {
      await api.adminUpdateChannelStatus(id, status)
      notifications.show({ color: 'green', message: t('admin.channelUpdated') })
      reload()
    } catch (e) {
      notifyError('admin.updateFailed', e)
    }
  }

  async function confirmDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      await api.adminDeleteChannel(deleteTarget.id)
      notifications.show({ color: 'green', message: t('admin.channelDeleted') })
      setDeleteTarget(null)
      reload()
    } catch (e) {
      notifyError('admin.deleteFailed', e)
    } finally {
      setDeleting(false)
    }
  }

  return (
    <AdminPageShell title={t('admin.headerChannels')}>
      <Modal opened={deleteTarget !== null} onClose={() => setDeleteTarget(null)} title={t('common.delete')}>
        <Stack>
          <Text size="sm">
            <Trans
              i18nKey="admin.deleteChannelConfirm"
              values={{ name: deleteTarget?.name ?? '' }}
              components={{ bold: <b /> }}
            />
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setDeleteTarget(null)}>
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
                    <Text style={{ cursor: 'pointer' }} fw={500} onClick={() => navigate(`/admin/channels/${c.id}`)}>
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
                          <Menu.Item onClick={() => setStatus(c.id, 'active')}>{t('admin.enable')}</Menu.Item>
                        ) : (
                          <Menu.Item onClick={() => setStatus(c.id, 'disabled')}>{t('admin.disable')}</Menu.Item>
                        )}
                        <Menu.Divider />
                        <Menu.Item color="red" onClick={() => setDeleteTarget({ id: c.id, name: c.name })}>
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
          limit={ADMIN_PAGE_LIMIT}
          onPrev={() => setPage((p) => Math.max(1, p - 1))}
          onNext={() => setPage((p) => p + 1)}
        />
      </Stack>
    </AdminPageShell>
  )
}
