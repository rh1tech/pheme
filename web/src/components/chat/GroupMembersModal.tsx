import { useEffect, useState } from 'react'
import { ActionIcon, Avatar, Badge, Group, Loader, Menu, Stack, Text, TextInput, UnstyledButton } from '@mantine/core'
import { IconDots, IconSearch, IconUserPlus, IconWorld } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { api, imageUrl } from '../../lib/api'
import { addGroupMember, removeGroupMember, PeerKeysMissingError } from '../../lib/mls'
import { notifyError, notifySuccess } from '../../lib/notify'
import { userLabel } from '../../lib/conversation'
import { useAuth } from '../../auth/context'
import { ResponsiveModal } from '../ResponsiveModal'
import { UserInfoModal } from './UserInfoModal'
import type { Conversation, PublicUser } from '../../lib/types'

interface GroupMembersModalProps {
  conversation: Conversation
  opened: boolean
  onClose: () => void
  /** Re-fetch the conversation after any membership or role change. */
  onChanged: () => Promise<void>
}

/**
 * The member roster of a group: who is in it and their role, with add / remove /
 * promote / demote for admins. Adding and removing a member also drives the MLS group
 * (a Welcome + Commit, or a removal Commit) so the encryption tracks the membership;
 * the role change is server-only, since a role is not a cryptographic property.
 */
export function GroupMembersModal({ conversation, opened, onClose, onChanged }: GroupMembersModalProps) {
  const { t } = useTranslation()
  const { userId } = useAuth()
  const [busy, setBusy] = useState<string | null>(null) // userId being acted on
  const [adding, setAdding] = useState(false)
  // The member whose contact card is open, tapped from their avatar in the roster.
  const [viewing, setViewing] = useState<PublicUser | null>(null)

  const me = conversation.members.find((m) => m.userId === userId)
  const iAmAdmin = me?.role === 'admin'
  const memberIds = new Set(conversation.members.map((m) => m.userId))

  async function act(targetId: string, fn: () => Promise<void>, successKey: string) {
    if (!userId) return
    setBusy(targetId)
    try {
      await fn()
      await onChanged()
      notifySuccess(t(successKey))
    } catch (e) {
      if (e instanceof PeerKeysMissingError) notifyError(t('chat.peerNotReady'), null)
      else notifyError(t('group.actionFailed'), e)
    } finally {
      setBusy(null)
    }
  }

  return (
    <ResponsiveModal opened={opened} onClose={onClose} title={t('group.membersTitle')}>
      <Stack gap="sm">
        {iAmAdmin && (
          <>
            <Text size="xs" c="dimmed" fw={600} tt="uppercase">
              {t('group.addMember')}
            </Text>
            <AddMemberSearch
              exclude={memberIds}
              busy={adding}
              onPick={async (u) => {
                setAdding(true)
                await act(u.id, () => addGroupMember(conversation.id, userId ?? '', u.id), 'group.added')
                setAdding(false)
              }}
              onAddHandle={async (handle) => {
                setAdding(true)
                await act(handle, () => addGroupMember(conversation.id, userId ?? '', handle), 'group.added')
                setAdding(false)
              }}
            />
          </>
        )}

        <Text size="xs" c="dimmed" fw={600} tt="uppercase">
          {t('group.members', { count: conversation.members.length })}
        </Text>
        <Stack gap={2}>
          {conversation.members.map((m) => {
            const isMe = m.userId === userId
            const isOwner = m.userId === conversation.createdBy
            return (
              <Group key={m.userId} gap="sm" wrap="nowrap" py={4}>
                <UnstyledButton
                  onClick={() => setViewing(m.user)}
                  aria-label={t('chat.openInfo')}
                  style={{ borderRadius: 9999, flex: '0 0 auto', display: 'inline-flex' }}
                >
                  <Avatar
                    src={m.user.avatarId ? imageUrl(m.user.avatarId) : undefined}
                    radius="xl"
                    size={34}
                    color="iris"
                  >
                    {userLabel(m.user).slice(0, 2).toUpperCase()}
                  </Avatar>
                </UnstyledButton>
                <Stack gap={0} style={{ flex: 1, minWidth: 0 }}>
                  <Text size="sm" truncate>
                    {userLabel(m.user)}
                    {isMe && ` (${t('group.you')})`}
                  </Text>
                </Stack>
                {m.role === 'admin' && (
                  <Badge size="sm" variant="light" color="iris">
                    {t('group.admin')}
                  </Badge>
                )}
                {/* An admin can act on everyone but the owner and themselves. */}
                {iAmAdmin && !isOwner && !isMe && (
                  <Menu position="bottom-end" withinPortal>
                    <Menu.Target>
                      <ActionIcon
                        variant="subtle"
                        color="gray"
                        aria-label={`${t('group.memberActions')}: ${userLabel(m.user)}`}
                        loading={busy === m.userId}
                      >
                        <IconDots size={18} />
                      </ActionIcon>
                    </Menu.Target>
                    <Menu.Dropdown>
                      {m.role === 'user' ? (
                        <Menu.Item
                          onClick={() =>
                            act(m.userId, () => api.setConversationMemberRole(conversation.id, m.userId, 'admin'), 'group.roleChanged')
                          }
                        >
                          {t('group.makeAdmin')}
                        </Menu.Item>
                      ) : (
                        <Menu.Item
                          onClick={() =>
                            act(m.userId, () => api.setConversationMemberRole(conversation.id, m.userId, 'user'), 'group.roleChanged')
                          }
                        >
                          {t('group.removeAdmin')}
                        </Menu.Item>
                      )}
                      <Menu.Item
                        color="red"
                        onClick={() => act(m.userId, () => removeGroupMember(conversation.id, userId ?? '', m.userId), 'group.removed')}
                      >
                        {t('group.remove')}
                      </Menu.Item>
                    </Menu.Dropdown>
                  </Menu>
                )}
              </Group>
            )
          })}
        </Stack>
      </Stack>

      {/* A member's contact card, opened from their avatar. Rendered here so it
          stacks above the roster sheet. */}
      <UserInfoModal
        user={viewing}
        opened={viewing !== null}
        onClose={() => setViewing(null)}
      />
    </ResponsiveModal>
  )
}

interface AddMemberSearchProps {
  exclude: Set<string>
  busy: boolean
  onPick: (user: PublicUser) => void
  onAddHandle: (handle: string) => void
}

// A `username@host` handle for someone on another server. Local user search never
// returns these — the directory a search hits is one host's — so a federated member
// is added by typing their full handle. A dotted host distinguishes it from a stray
// '@' in a name query.
const REMOTE_HANDLE = /^[a-zA-Z0-9_.]{3,30}@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/

/** A debounced user search that lists people not already in the group. */
function AddMemberSearch({ exclude, busy, onPick, onAddHandle }: AddMemberSearchProps) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<PublicUser[]>([])
  const [searching, setSearching] = useState(false)

  const trimmed = query.trim()
  const remoteHandle = REMOTE_HANDLE.test(trimmed) ? trimmed : null

  useEffect(() => {
    const q = query.trim()
    if (q.length < 2) return // too short: the render hides stale results
    const handle = window.setTimeout(() => {
      setSearching(true)
      api
        .searchUsers(q)
        .then((users) => setResults(users.filter((u) => !exclude.has(u.id))))
        .catch(() => setResults([]))
        .finally(() => setSearching(false))
    }, 250)
    return () => window.clearTimeout(handle)
  }, [query, exclude])

  const shown = trimmed.length < 2 ? [] : results

  return (
    <Stack gap="xs">
      <TextInput
        placeholder={t('chat.searchPeople')}
        value={query}
        onChange={(e) => setQuery(e.currentTarget.value)}
        leftSection={<IconSearch size={16} stroke={1.8} />}
        rightSection={searching || busy ? <Loader size="xs" /> : null}
        disabled={busy}
        type="search"
        autoComplete="off"
        autoCorrect="off"
        autoCapitalize="none"
        spellCheck={false}
        enterKeyHint="search"
      />
      {remoteHandle && (
        <UnstyledButton
          onClick={() => onAddHandle(remoteHandle)}
          disabled={busy}
          aria-label={t('group.addRemote', { handle: remoteHandle })}
          p="xs"
          style={{ borderRadius: 'var(--mantine-radius-md)' }}
          className="pheme-user-pick"
        >
          <Group gap="sm" wrap="nowrap">
            <Avatar radius="xl" size={30} color="iris">
              <IconWorld size={16} />
            </Avatar>
            <Text size="sm" truncate>
              {t('group.addRemote', { handle: remoteHandle })}
            </Text>
            <IconUserPlus size={16} style={{ marginLeft: 'auto', flex: '0 0 auto' }} />
          </Group>
        </UnstyledButton>
      )}
      {shown.map((u) => (
        <UnstyledButton
          key={u.id}
          onClick={() => onPick(u)}
          disabled={busy}
          aria-label={t('group.addName', { name: userLabel(u) })}
          p="xs"
          style={{ borderRadius: 'var(--mantine-radius-md)' }}
          className="pheme-user-pick"
        >
          <Group gap="sm" wrap="nowrap">
            <Avatar src={u.avatarId ? imageUrl(u.avatarId) : undefined} radius="xl" size={30} color="iris">
              {userLabel(u).slice(0, 2).toUpperCase()}
            </Avatar>
            <Text size="sm">{userLabel(u)}</Text>
            <IconUserPlus size={16} style={{ marginLeft: 'auto' }} />
          </Group>
        </UnstyledButton>
      ))}
    </Stack>
  )
}
