import { useEffect, useState } from 'react'
import { ActionIcon, Group, Menu, Table, Text } from '@mantine/core'
import { IconDots } from '@tabler/icons-react'
import { Trans, useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { notifyError, notifySuccess } from '../../lib/notify'
import type { AdminComment, Paged } from '../../lib/types'
import { ADMIN_PAGE_LIMIT, AdminPageShell, Pager, SearchBar } from '../../components/admin/AdminUI'
import { ConfirmModal } from '../../components/ConfirmModal'
import { TableRowsSkeleton } from '../../components/Skeletons'

type CommentTarget = { id: string; email: string }
type BanTarget = { id: string; email: string }

export function AdminCommentsPage() {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(1)
  const [comments, setComments] = useState<Paged<AdminComment> | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<CommentTarget | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [banTarget, setBanTarget] = useState<BanTarget | null>(null)
  const [banning, setBanning] = useState(false)

  function reload() {
    api
      .adminListComments(query, page, ADMIN_PAGE_LIMIT)
      .then(setComments)
      .catch((e) => notifyError(t('admin.loadFailed'), e))
  }

  useEffect(() => {
    let active = true
    api
      .adminListComments(query, page, ADMIN_PAGE_LIMIT)
      .then((p) => active && setComments(p))
      .catch((e) => active && notifyError(t('admin.loadFailed'), e))
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, page])

  async function confirmDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      await api.adminDeleteComment(deleteTarget.id)
      notifySuccess(t('admin.commentDeleted'))
      setDeleteTarget(null)
      reload()
    } catch (e) {
      notifyError(t('admin.deleteFailed'), e)
    } finally {
      setDeleting(false)
    }
  }

  async function confirmBan() {
    if (!banTarget) return
    setBanning(true)
    try {
      await api.adminUpdateUser(banTarget.id, { status: 'blocked' })
      notifySuccess(t('admin.authorBanned'))
      setBanTarget(null)
    } catch (e) {
      notifyError(t('admin.banFailed'), e)
    } finally {
      setBanning(false)
    }
  }

  return (
    <AdminPageShell
      title={t('admin.headerComments')}
      actions={
        <SearchBar
          value={search}
          onChange={setSearch}
          onSubmit={() => {
            setPage(1)
            setQuery(search.trim())
          }}
          placeholder={t('admin.searchComments')}
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
          <Trans
            i18nKey="admin.deleteCommentConfirm"
            values={{ email: deleteTarget?.email ?? '' }}
            components={{ bold: <b /> }}
          />
        </Text>
      </ConfirmModal>

      <ConfirmModal
        opened={banTarget !== null}
        onClose={() => setBanTarget(null)}
        onConfirm={confirmBan}
        title={t('admin.banAuthor')}
        confirmLabel={t('admin.banAuthor')}
        confirmColor="orange"
        loading={banning}
      >
        <Text size="sm">
          <Trans
            i18nKey="admin.banAuthorConfirm"
            values={{ email: banTarget?.email ?? '' }}
            components={{ bold: <b /> }}
          />
        </Text>
      </ConfirmModal>

      {comments && comments.items.length === 0 ? (
        <Text c="dimmed" size="sm">
          {t('admin.noComments')}
        </Text>
      ) : (
        <Table.ScrollContainer minWidth={760}>
          <Table verticalSpacing="xs" striped>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t('admin.colAuthor')}</Table.Th>
                <Table.Th>{t('admin.colChannel')}</Table.Th>
                <Table.Th>{t('admin.colMessage')}</Table.Th>
                <Table.Th>{t('admin.colComment')}</Table.Th>
                <Table.Th>{t('admin.colCreated')}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            {comments === null ? (
              <TableRowsSkeleton rows={ADMIN_PAGE_LIMIT} cols={6} />
            ) : (
              <Table.Tbody>
                {comments.items.map((c) => (
                  <Table.Tr key={c.id}>
                    <Table.Td>{c.authorEmail}</Table.Td>
                    <Table.Td>{c.channelName}</Table.Td>
                    <Table.Td>{c.messageTitle || t('channel.noTitle')}</Table.Td>
                    <Table.Td style={{ maxWidth: 320 }}>
                      <Text size="sm" lineClamp={2}>
                        {c.body}
                      </Text>
                    </Table.Td>
                    <Table.Td>{new Date(c.createdAt).toLocaleDateString()}</Table.Td>
                    <Table.Td align="right">
                      <Menu position="bottom-end" withinPortal>
                        <Menu.Target>
                          <ActionIcon variant="subtle" color="gray">
                            <IconDots size={16} />
                          </ActionIcon>
                        </Menu.Target>
                        <Menu.Dropdown>
                          <Menu.Item
                            color="orange"
                            onClick={() => setBanTarget({ id: c.authorId, email: c.authorEmail })}
                          >
                            {t('admin.banAuthor')}
                          </Menu.Item>
                          <Menu.Divider />
                          <Menu.Item
                            color="red"
                            onClick={() => setDeleteTarget({ id: c.id, email: c.authorEmail })}
                          >
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
        </Table.ScrollContainer>
      )}
      <Group justify="center">
        <Pager
          page={comments?.page ?? 1}
          total={comments?.total ?? 0}
          limit={ADMIN_PAGE_LIMIT}
          onPrev={() => setPage((p) => Math.max(1, p - 1))}
          onNext={() => setPage((p) => p + 1)}
        />
      </Group>
    </AdminPageShell>
  )
}
