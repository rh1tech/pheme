import { useEffect, useState } from 'react'
import { ActionIcon, Button, Card, Group, Menu, Stack, Table, Text } from '@mantine/core'
import { IconDots } from '@tabler/icons-react'
import { Trans, useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { memberLabel } from '../lib/conversation'
import { notifyError, notifySuccess } from '../lib/notify'
import type { Member } from '../lib/types'
import { ChannelRoleBadge, MemberStatusBadge } from './badges'
import { ConfirmModal } from './ConfirmModal'
import { CardListSkeleton } from './Skeletons'

const PAGE = 50

interface SubscribersPanelProps {
  channelId: string
}

/**
 * Owner/channel-admin view of a channel's pending approvals and subscriber list.
 * The member list is lazily paginated by offset ("Load more").
 */
export function SubscribersPanel({ channelId }: SubscribersPanelProps) {
  const { t } = useTranslation()
  const [pending, setPending] = useState<Member[]>([])
  const [members, setMembers] = useState<Member[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)
  const [confirmRemove, setConfirmRemove] = useState<Member | null>(null)
  const [removing, setRemoving] = useState(false)

  useEffect(() => {
    let active = true
    Promise.all([api.listApprovals(channelId), api.listMembers(channelId, 0, PAGE)])
      .then(([approvals, page]) => {
        if (!active) return
        setPending(approvals)
        setMembers(page.items)
        setTotal(page.total)
      })
      .catch((e) => active && notifyError(t('channel.subscribers.actionFailed'), e))
      .finally(() => active && setLoading(false))
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId])

  async function reload() {
    try {
      const [approvals, page] = await Promise.all([
        api.listApprovals(channelId),
        api.listMembers(channelId, 0, PAGE),
      ])
      setPending(approvals)
      setMembers(page.items)
      setTotal(page.total)
    } catch (e) {
      notifyError(t('channel.subscribers.actionFailed'), e)
    }
  }

  async function loadMore() {
    setLoadingMore(true)
    try {
      const page = await api.listMembers(channelId, members.length, PAGE)
      setMembers((prev) => [...prev, ...page.items])
      setTotal(page.total)
    } catch (e) {
      notifyError(t('channel.subscribers.actionFailed'), e)
    } finally {
      setLoadingMore(false)
    }
  }

  async function run(key: string, fn: () => Promise<unknown>, success: string) {
    setBusy(key)
    try {
      await fn()
      notifySuccess(success)
      await reload()
    } catch (e) {
      notifyError(t('channel.subscribers.actionFailed'), e)
    } finally {
      setBusy(null)
    }
  }

  const approve = (m: Member) =>
    run(`a:${m.userId}`, () => api.approveMember(channelId, m.userId), t('channel.subscribers.approved'))
  const deny = (m: Member) =>
    run(`d:${m.userId}`, () => api.denyMember(channelId, m.userId), t('channel.subscribers.denied'))
  const toggleRole = (m: Member) =>
    run(
      `r:${m.userId}`,
      () => api.updateMember(channelId, m.userId, { role: m.role === 'admin' ? 'user' : 'admin' }),
      t('channel.subscribers.roleChanged'),
    )
  const toggleBan = (m: Member) =>
    run(
      `b:${m.userId}`,
      () => api.updateMember(channelId, m.userId, { status: m.status === 'blocked' ? 'active' : 'blocked' }),
      m.status === 'blocked' ? t('channel.subscribers.unbanned') : t('channel.subscribers.banned'),
    )

  async function doRemove() {
    if (!confirmRemove) return
    setRemoving(true)
    try {
      await api.removeMember(channelId, confirmRemove.userId)
      notifySuccess(t('channel.subscribers.removed'))
      setConfirmRemove(null)
      await reload()
    } catch (e) {
      notifyError(t('channel.subscribers.actionFailed'), e)
    } finally {
      setRemoving(false)
    }
  }

  if (loading) return <CardListSkeleton rows={4} />

  return (
    <Stack gap="lg">
      <ConfirmModal
        opened={confirmRemove !== null}
        onClose={() => setConfirmRemove(null)}
        onConfirm={doRemove}
        title={t('channel.subscribers.remove')}
        confirmLabel={t('channel.subscribers.remove')}
        loading={removing}
      >
        <Text size="sm">
          <Trans
            i18nKey="channel.subscribers.removeConfirm"
            values={{ name: confirmRemove ? memberLabel(confirmRemove) : '' }}
            components={{ bold: <b /> }}
          />
        </Text>
      </ConfirmModal>

      <Stack gap="sm">
        <Text fw={600}>{t('channel.subscribers.pendingTitle')}</Text>
        {pending.length === 0 ? (
          <Text c="dimmed" size="sm">
            {t('channel.subscribers.noPending')}
          </Text>
        ) : (
          pending.map((m) => (
            <Card key={m.userId} withBorder padding="sm">
              <Group justify="space-between" wrap="nowrap">
                <Text size="sm" style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>
                  {memberLabel(m)}
                </Text>
                <Group gap="xs" wrap="nowrap">
                  <Button
                    size="compact-sm"
                    color="teal"
                    loading={busy === `a:${m.userId}`}
                    onClick={() => approve(m)}
                  >
                    {t('channel.subscribers.approve')}
                  </Button>
                  <Button
                    size="compact-sm"
                    variant="subtle"
                    color="red"
                    loading={busy === `d:${m.userId}`}
                    onClick={() => deny(m)}
                  >
                    {t('channel.subscribers.deny')}
                  </Button>
                </Group>
              </Group>
            </Card>
          ))
        )}
      </Stack>

      <Stack gap="sm">
        <Text fw={600}>{t('channel.subscribers.membersTitle')}</Text>
        {members.length === 0 ? (
          <Text c="dimmed" size="sm">
            {t('channel.subscribers.noMembers')}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={420}>
            <Table verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t('channel.subscribers.colMember')}</Table.Th>
                  <Table.Th>{t('channel.subscribers.colRole')}</Table.Th>
                  <Table.Th>{t('channel.subscribers.colStatus')}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {members.map((m) => (
                  <Table.Tr key={m.userId}>
                    <Table.Td>{memberLabel(m)}</Table.Td>
                    <Table.Td>
                      <ChannelRoleBadge role={m.role} />
                    </Table.Td>
                    <Table.Td>
                      <MemberStatusBadge status={m.status} />
                    </Table.Td>
                    <Table.Td align="right">
                      <Menu position="bottom-end" withinPortal>
                        <Menu.Target>
                          <ActionIcon variant="subtle" color="gray" aria-label={t('admin.actions')} loading={busy?.endsWith(m.userId)}>
                            <IconDots size={18} />
                          </ActionIcon>
                        </Menu.Target>
                        <Menu.Dropdown>
                          <Menu.Item onClick={() => toggleRole(m)}>
                            {m.role === 'admin' ? t('channel.subscribers.makeUser') : t('channel.subscribers.makeAdmin')}
                          </Menu.Item>
                          <Menu.Item color={m.status === 'blocked' ? undefined : 'orange'} onClick={() => toggleBan(m)}>
                            {m.status === 'blocked' ? t('channel.subscribers.unban') : t('channel.subscribers.ban')}
                          </Menu.Item>
                          <Menu.Item color="red" onClick={() => setConfirmRemove(m)}>
                            {t('channel.subscribers.remove')}
                          </Menu.Item>
                        </Menu.Dropdown>
                      </Menu>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
        {members.length < total && (
          <Group justify="center">
            <Button variant="subtle" loading={loadingMore} onClick={loadMore}>
              {t('channel.subscribers.loadMore')}
            </Button>
          </Group>
        )}
      </Stack>
    </Stack>
  )
}
