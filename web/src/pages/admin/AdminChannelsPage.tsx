import { useEffect, useState } from 'react'
import { ActionIcon, Menu, Table, Text } from '@mantine/core'
import { IconDots } from '@tabler/icons-react'
import { Trans, useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { api } from '../../lib/api'
import { notifyError, notifySuccess } from '../../lib/notify'
import type { AdminChannel, ChannelStatus, Paged } from '../../lib/types'
import { ADMIN_PAGE_LIMIT, AdminPageShell, Pager, SearchBar } from '../../components/admin/AdminUI'
import { ConfirmModal } from '../../components/ConfirmModal'
import { ChannelStatusBadge, ModeBadge } from '../../components/badges'
import { TableRowsSkeleton } from '../../components/Skeletons'

export function AdminChannelsPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(1)
  const [channels, setChannels] = useState<Paged<AdminChannel> | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; name: string } | null>(null)
  const [deleting, setDeleting] = useState(false)

  function reload() {
    api
      .adminListChannels(query, page, ADMIN_PAGE_LIMIT)
      .then(setChannels)
      .catch((e) => notifyError(t('admin.loadFailed'), e))
  }

  useEffect(() => {
    let active = true
    api
      .adminListChannels(query, page, ADMIN_PAGE_LIMIT)
      .then((p) => active && setChannels(p))
      .catch((e) => active && notifyError(t('admin.loadFailed'), e))
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, page])

  async function setStatus(id: string, status: ChannelStatus) {
    try {
      await api.adminUpdateChannelStatus(id, status)
      notifySuccess(t('admin.channelUpdated'))
      reload()
    } catch (e) {
      notifyError(t('admin.updateFailed'), e)
    }
  }

  async function confirmDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      await api.adminDeleteChannel(deleteTarget.id)
      notifySuccess(t('admin.channelDeleted'))
      setDeleteTarget(null)
      reload()
    } catch (e) {
      notifyError(t('admin.deleteFailed'), e)
    } finally {
      setDeleting(false)
    }
  }

  return (
    <AdminPageShell
      title={t('admin.headerChannels')}
      actions={
        <SearchBar
          value={search}
          onChange={setSearch}
          onSubmit={() => {
            setPage(1)
            setQuery(search.trim())
          }}
          placeholder={t('admin.searchChannels')}
        />
      }
    >
      <ConfirmModal
        opened={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={confirmDelete}
        title={t('common.delete')}
        loading={deleting}
      >
        <Text size="sm">
          <Trans i18nKey="admin.deleteChannelConfirm" values={{ name: deleteTarget?.name ?? '' }} components={{ bold: <b /> }} />
        </Text>
      </ConfirmModal>

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
          {channels === null ? (
            <TableRowsSkeleton rows={ADMIN_PAGE_LIMIT} cols={5} />
          ) : (
            <Table.Tbody>
              {channels.items.map((c) => (
              <Table.Tr key={c.id}>
                <Table.Td>
                  <Text style={{ cursor: 'pointer' }} fw={500} onClick={() => navigate(`/admin/channels/${c.id}`)}>
                    {c.name}
                  </Text>
                </Table.Td>
                <Table.Td>{c.ownerEmail}</Table.Td>
                <Table.Td>
                  <ModeBadge mode={c.subscriptionMode} />
                </Table.Td>
                <Table.Td>
                  <ChannelStatusBadge status={c.status} />
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
          )}
        </Table>
      )}
      <Pager
        page={channels?.page ?? 1}
        total={channels?.total ?? 0}
        limit={ADMIN_PAGE_LIMIT}
        onPrev={() => setPage((p) => Math.max(1, p - 1))}
        onNext={() => setPage((p) => p + 1)}
      />
    </AdminPageShell>
  )
}
