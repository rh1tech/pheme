import { useEffect, useRef, useState } from 'react'
import {
  Avatar,
  Button,
  Group,
  Loader,
  Pill,
  Stack,
  Text,
  TextInput,
  UnstyledButton,
} from '@mantine/core'
import { IconSearch, IconWorld } from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api, imageUrl } from '../../lib/api'
import { notifyError } from '../../lib/notify'
import { userLabel } from '../../lib/conversation'
import { remoteHandle } from '../../lib/handles'
import { ResponsiveModal } from '../ResponsiveModal'
import type { PublicUser } from '../../lib/types'

interface UserSearchProps {
  onPick: (user: PublicUser) => void
  exclude: Set<string>
  // When set, a `username@host` query offers to start a cross-host chat with them.
  // Only the direct-chat flow provides it; group creation adds remote members
  // afterward (createConversation does not provision remote mirrors).
  onPickHandle?: (handle: string) => void
}

/** A debounced user search that lists public profiles to pick from. */
function UserSearch({ onPick, exclude, onPickHandle }: UserSearchProps) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<PublicUser[]>([])
  const [searching, setSearching] = useState(false)

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

  const tooShort = query.trim().length < 2
  const shownResults = tooShort ? [] : results
  const handle = onPickHandle ? remoteHandle(query) : null

  return (
    <Stack gap="xs">
      <TextInput
        data-autofocus
        placeholder={t('chat.searchPeople')}
        value={query}
        onChange={(e) => setQuery(e.currentTarget.value)}
        leftSection={<IconSearch size={16} stroke={1.8} />}
        rightSection={searching ? <Loader size="xs" /> : null}
        type="search"
        autoComplete="off"
        autoCorrect="off"
        autoCapitalize="none"
        spellCheck={false}
        enterKeyHint="search"
      />
      {/* No inner maxHeight/scroller: the results flow inside the modal body, which
          already scrolls and is bounded to the visible viewport (ResponsiveModal). A
          fixed 260px box nested a second scrollbar and cropped the list short. */}
      <Stack gap={2}>
        {handle && (
          <UnstyledButton
            onClick={() => onPickHandle?.(handle)}
            p="xs"
            style={{ borderRadius: 'var(--mantine-radius-md)' }}
            className="pheme-user-pick"
          >
            <Group gap="sm" wrap="nowrap">
              <Avatar radius="xl" size={32} color="iris">
                <IconWorld size={18} />
              </Avatar>
              <Text size="sm" truncate>
                {t('chat.startRemote', { handle })}
              </Text>
            </Group>
          </UnstyledButton>
        )}
        {shownResults.map((u) => (
          <UnstyledButton
            key={u.id}
            onClick={() => onPick(u)}
            p="xs"
            style={{ borderRadius: 'var(--mantine-radius-md)' }}
            className="pheme-user-pick"
          >
            <Group gap="sm" wrap="nowrap">
              <Avatar src={u.avatarId ? imageUrl(u.avatarId) : undefined} radius="xl" size={32} color="iris">
                {userLabel(u).slice(0, 2).toUpperCase()}
              </Avatar>
              <Text size="sm">{userLabel(u)}</Text>
            </Group>
          </UnstyledButton>
        ))}
        {query.trim().length >= 2 && !searching && results.length === 0 && !handle && (
          <Text c="dimmed" size="sm" ta="center" py="sm">
            {t('chat.noPeople')}
          </Text>
        )}
      </Stack>
    </Stack>
  )
}

interface NewChatModalsProps {
  directOpen: boolean
  groupOpen: boolean
  onClose: () => void
  onChanged: () => Promise<void>
}

/** The "start a chat" and "create a group" flows, opened from the + menu. */
export function NewChatModals({ directOpen, groupOpen, onClose, onChanged }: NewChatModalsProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [creating, setCreating] = useState(false)
  const [title, setTitle] = useState('')
  const [picked, setPicked] = useState<PublicUser[]>([])
  const titleRef = useRef<HTMLInputElement>(null)

  function reset() {
    setTitle('')
    setPicked([])
  }

  async function startDirect(user: PublicUser) {
    setCreating(true)
    try {
      const conv = await api.createDirectChat(user.id)
      await onChanged()
      onClose()
      reset()
      navigate(`/chats/${conv.id}`)
    } catch (e) {
      notifyError(t('chat.startFailed'), e)
    } finally {
      setCreating(false)
    }
  }

  // A cross-host chat cannot be a direct chat — those are a fixed local pair and
  // the server has no mirror for them. So starting one with someone on another host
  // makes a two-person group instead: create it empty, record the remote member
  // (the server resolves the handle and provisions the mirror), then open it — the
  // MLS group is established and their key package claimed on open, as for any new
  // group.
  async function startWithRemote(handle: string) {
    setCreating(true)
    try {
      const conv = await api.createGroupChat(handle.split('@')[0], [])
      await api.addConversationMember(conv.id, handle)
      await onChanged()
      onClose()
      reset()
      navigate(`/chats/${conv.id}`)
    } catch (e) {
      notifyError(t('chat.startFailed'), e)
    } finally {
      setCreating(false)
    }
  }

  async function createGroup() {
    if (picked.length === 0 || !title.trim()) return
    setCreating(true)
    try {
      const conv = await api.createGroupChat(
        title.trim(),
        picked.map((u) => u.id),
      )
      await onChanged()
      onClose()
      reset()
      navigate(`/chats/${conv.id}`)
    } catch (e) {
      notifyError(t('chat.startFailed'), e)
    } finally {
      setCreating(false)
    }
  }

  const excluded = new Set(picked.map((u) => u.id))

  return (
    <>
      <ResponsiveModal opened={directOpen} onClose={onClose} title={t('chat.newChatTitle')}>
        <UserSearch onPick={startDirect} onPickHandle={startWithRemote} exclude={new Set()} />
        {creating && <Loader size="sm" mt="sm" />}
      </ResponsiveModal>

      <ResponsiveModal
        opened={groupOpen}
        onClose={onClose}
        title={t('chat.newGroupTitle')}
        onEnterTransitionEnd={() => titleRef.current?.focus()}
      >
        <Stack gap="sm">
          <TextInput
            ref={titleRef}
            label={t('chat.groupName')}
            placeholder={t('chat.groupNamePlaceholder')}
            value={title}
            onChange={(e) => setTitle(e.currentTarget.value)}
          />
          {picked.length > 0 && (
            <Group gap={4}>
              {picked.map((u) => (
                <Pill key={u.id} withRemoveButton onRemove={() => setPicked((p) => p.filter((x) => x.id !== u.id))}>
                  {userLabel(u)}
                </Pill>
              ))}
            </Group>
          )}
          <UserSearch onPick={(u) => setPicked((p) => [...p, u])} exclude={excluded} />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button
              onClick={createGroup}
              loading={creating}
              disabled={picked.length === 0 || !title.trim()}
            >
              {t('chat.createGroup')}
            </Button>
          </Group>
        </Stack>
      </ResponsiveModal>
    </>
  )
}
