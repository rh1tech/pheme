import { useEffect, useState } from 'react'
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Code,
  CopyButton,
  Group,
  Menu,
  Modal,
  NumberInput,
  Stack,
  Table,
  Text,
  TextInput,
} from '@mantine/core'
import { IconCheck, IconCopy, IconDots, IconPlus } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { apiBase } from '../../lib/server'
import { notifyError, notifySuccess } from '../../lib/notify'
import type { AdminInvite, Paged } from '../../lib/types'
import { ADMIN_PAGE_LIMIT, AdminPageShell, Pager, SearchBar } from '../../components/admin/AdminUI'
import { ConfirmModal } from '../../components/ConfirmModal'
import { TableRowsSkeleton } from '../../components/Skeletons'

/// The link an invitee opens in a browser. It points at THIS panel's origin rather than at the
/// API, because what the invitee needs is the sign-up form; the form finds its own server from
/// there.
function inviteLink(code: string): string {
  return `${window.location.origin}/login?invite=${encodeURIComponent(code)}`
}

/// The same invitation, for a phone with the app installed.
///
/// A private scheme rather than an https App Link, and it has to carry the server with it: Pheme
/// is self-hosted, so a fresh install has no idea which server an invitation belongs to, and the
/// browser link's origin is exactly the piece of information the app is missing. See
/// mobile/lib/src/core/deep_links.dart.
function inviteAppLink(code: string, server: string): string {
  const params = new URLSearchParams({ code })
  if (server) params.set('server', server)
  return `pheme://invite?${params.toString()}`
}

const STATUS_COLORS: Record<AdminInvite['status'], string> = {
  pending: 'green',
  used: 'gray',
  revoked: 'red',
  expired: 'orange',
}

export function AdminInvitesPage() {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(1)
  const [invites, setInvites] = useState<Paged<AdminInvite> | null>(null)
  const [inviteOnly, setInviteOnly] = useState(true)
  const [creating, setCreating] = useState(false)
  const [formOpen, setFormOpen] = useState(false)
  const [note, setNote] = useState('')
  const [expiresInDays, setExpiresInDays] = useState<number | string>(0)
  // The freshly minted invitation, held only until this modal closes. There is nowhere to
  // fetch it back from — the server kept a hash — so closing without copying loses the link.
  const [minted, setMinted] = useState<AdminInvite | null>(null)
  const [revokeTarget, setRevokeTarget] = useState<AdminInvite | null>(null)
  const [revoking, setRevoking] = useState(false)
  const [reloadToken, setReloadToken] = useState(0)

  useEffect(() => {
    let active = true
    api
      .adminListInvites(query, page, ADMIN_PAGE_LIMIT)
      .then((p) => active && setInvites(p))
      .catch((e) => active && notifyError(t('admin.loadFailed'), e))
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, page, reloadToken])

  // Whether invitations are actually being enforced. An admin generating links on a server
  // that still takes open registrations should be told so, rather than left believing the
  // door is shut.
  useEffect(() => {
    let active = true
    api
      .registrationInfo()
      .then((info) => active && setInviteOnly(info.inviteOnly))
      .catch(() => undefined)
    return () => {
      active = false
    }
  }, [])

  async function create() {
    setCreating(true)
    try {
      const days = typeof expiresInDays === 'number' ? expiresInDays : Number(expiresInDays) || 0
      const created = await api.adminCreateInvite({ note: note.trim(), expiresInDays: days })
      setMinted(created)
      setFormOpen(false)
      setNote('')
      setExpiresInDays(0)
      notifySuccess(t('admin.inviteCreated'))
      setReloadToken((n) => n + 1)
    } catch (e) {
      notifyError(t('admin.inviteCreateFailed'), e)
    } finally {
      setCreating(false)
    }
  }

  async function confirmRevoke() {
    if (!revokeTarget) return
    setRevoking(true)
    try {
      await api.adminRevokeInvite(revokeTarget.id)
      notifySuccess(t('admin.inviteRevoked'))
      setRevokeTarget(null)
      setReloadToken((n) => n + 1)
    } catch (e) {
      notifyError(t('admin.inviteRevokeFailed'), e)
    } finally {
      setRevoking(false)
    }
  }

  return (
    <AdminPageShell
      title={t('admin.inviteHeader')}
      actions={
        <Group gap="sm">
          <SearchBar
            value={search}
            onChange={setSearch}
            onSubmit={() => {
              setPage(1)
              setQuery(search.trim())
            }}
            placeholder={t('admin.inviteSearch')}
          />
          <Button leftSection={<IconPlus size={16} />} onClick={() => setFormOpen(true)}>
            {t('admin.inviteNew')}
          </Button>
        </Group>
      }
    >
      {!inviteOnly && (
        <Alert color="orange" variant="light">
          {t('admin.inviteOpenRegistration')}
        </Alert>
      )}

      <Modal opened={formOpen} onClose={() => setFormOpen(false)} title={t('admin.inviteNew')} centered>
        <Stack>
          <TextInput
            label={t('admin.inviteNote')}
            placeholder={t('admin.inviteNotePlaceholder')}
            value={note}
            maxLength={200}
            onChange={(e) => setNote(e.currentTarget.value)}
            autoFocus
          />
          <NumberInput
            label={t('admin.inviteExpiry')}
            description={t('admin.inviteNeverExpires')}
            value={expiresInDays}
            onChange={setExpiresInDays}
            min={0}
            max={365}
            suffix=" d"
          />
          <Button onClick={create} loading={creating}>
            {t('admin.inviteCreate')}
          </Button>
        </Stack>
      </Modal>

      <Modal opened={minted !== null} onClose={() => setMinted(null)} title={t('admin.inviteLink')} centered>
        <Stack>
          <Alert color="yellow" variant="light">
            {t('admin.inviteCopyOnce')}
          </Alert>
          <Stack gap="xs">
            <Text size="sm" fw={500}>
              {t('admin.inviteLinkWeb')}
            </Text>
            <Code block style={{ wordBreak: 'break-all' }}>
              {minted?.code ? inviteLink(minted.code) : ''}
            </Code>
            <CopyButton value={minted?.code ? inviteLink(minted.code) : ''}>
              {({ copied, copy }) => (
                <Button
                  leftSection={copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
                  color={copied ? 'green' : undefined}
                  onClick={copy}
                  variant="filled"
                >
                  {copied ? t('admin.inviteCopied') : t('admin.inviteCopy')}
                </Button>
              )}
            </CopyButton>
          </Stack>

          <Stack gap="xs">
            <Text size="sm" fw={500}>
              {t('admin.inviteLinkApp')}
            </Text>
            <Text size="xs" c="dimmed">
              {t('admin.inviteLinkAppHint')}
            </Text>
            <Code block style={{ wordBreak: 'break-all' }}>
              {minted?.code ? inviteAppLink(minted.code, apiBase()) : ''}
            </Code>
            <CopyButton value={minted?.code ? inviteAppLink(minted.code, apiBase()) : ''}>
              {({ copied, copy }) => (
                <Button
                  leftSection={copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
                  color={copied ? 'green' : undefined}
                  onClick={copy}
                  variant="default"
                >
                  {copied ? t('admin.inviteCopied') : t('admin.inviteCopy')}
                </Button>
              )}
            </CopyButton>
          </Stack>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={revokeTarget !== null}
        onClose={() => setRevokeTarget(null)}
        onConfirm={confirmRevoke}
        title={t('admin.inviteRevoke')}
        confirmLabel={t('admin.inviteRevoke')}
        confirmColor="red"
        loading={revoking}
      >
        <Text size="sm">{t('admin.inviteRevokeConfirm')}</Text>
      </ConfirmModal>

      {invites && invites.items.length === 0 ? (
        <Text c="dimmed" size="sm">
          {t('admin.inviteNone')}
        </Text>
      ) : (
        <Table.ScrollContainer minWidth={720}>
          <Table verticalSpacing="xs" striped>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t('admin.inviteColCode')}</Table.Th>
                <Table.Th>{t('admin.inviteColNote')}</Table.Th>
                <Table.Th>{t('admin.inviteColStatus')}</Table.Th>
                <Table.Th>{t('admin.inviteColCreated')}</Table.Th>
                <Table.Th>{t('admin.inviteColExpires')}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            {invites === null ? (
              <TableRowsSkeleton rows={ADMIN_PAGE_LIMIT} cols={6} />
            ) : (
              <Table.Tbody>
                {invites.items.map((i) => (
                  <Table.Tr key={i.id}>
                    <Table.Td>
                      <Code>{i.prefix}…</Code>
                    </Table.Td>
                    <Table.Td>{i.note || '—'}</Table.Td>
                    <Table.Td>
                      <Badge color={STATUS_COLORS[i.status]} variant="light">
                        {t(`admin.inviteStatus${i.status.charAt(0).toUpperCase()}${i.status.slice(1)}`)}
                      </Badge>
                    </Table.Td>
                    <Table.Td>{new Date(i.createdAt).toLocaleDateString()}</Table.Td>
                    <Table.Td>
                      {i.expiresAt
                        ? new Date(i.expiresAt).toLocaleDateString()
                        : t('admin.inviteNeverExpires')}
                    </Table.Td>
                    <Table.Td align="right">
                      {i.status === 'pending' && (
                        <Menu position="bottom-end" withinPortal>
                          <Menu.Target>
                            <ActionIcon variant="subtle" color="gray">
                              <IconDots size={16} />
                            </ActionIcon>
                          </Menu.Target>
                          <Menu.Dropdown>
                            <Menu.Item color="red" onClick={() => setRevokeTarget(i)}>
                              {t('admin.inviteRevoke')}
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
      <Group justify="center">
        <Pager
          page={invites?.page ?? 1}
          total={invites?.total ?? 0}
          limit={ADMIN_PAGE_LIMIT}
          onPrev={() => setPage((p) => Math.max(1, p - 1))}
          onNext={() => setPage((p) => p + 1)}
        />
      </Group>
    </AdminPageShell>
  )
}
