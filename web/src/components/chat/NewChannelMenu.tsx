import { useRef, useState } from 'react'
import {
  ActionIcon,
  Button,
  Group,
  Menu,
  SegmentedControl,
  Stack,
  Text,
  TextInput,
} from '@mantine/core'
import { IconMessagePlus, IconPencilPlus, IconPlus, IconUsersGroup, IconUserPlus } from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { notifyError, notifySuccess } from '../../lib/notify'
import { ResponsiveModal } from '../ResponsiveModal'
import { NewChatModals } from './NewChatModals'
import type { SubscriptionMode } from '../../lib/types'

interface NewChannelMenuProps {
  /** Re-reads the chat list after a channel is created or joined. */
  onChanged: () => Promise<void>
}

/**
 * The "+" in the chat-list header: create a channel, or join one by trigger ID /
 * phetag. Both flows previously lived on the dashboard.
 */
export function NewChannelMenu({ onChanged }: NewChannelMenuProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()

  const [createOpen, setCreateOpen] = useState(false)
  const [name, setName] = useState('')
  const [mode, setMode] = useState<SubscriptionMode>('approval')
  const [creating, setCreating] = useState(false)
  const nameRef = useRef<HTMLInputElement>(null)

  const [joinOpen, setJoinOpen] = useState(false)
  const [joinRef, setJoinRef] = useState('')
  const [joining, setJoining] = useState(false)
  const [directOpen, setDirectOpen] = useState(false)
  const [groupOpen, setGroupOpen] = useState(false)
  const joinInputRef = useRef<HTMLInputElement>(null)

  function openCreate() {
    setName('')
    setMode('approval')
    setCreateOpen(true)
  }

  function openJoin() {
    setJoinRef('')
    setJoinOpen(true)
  }

  async function createChannel() {
    if (!name.trim()) return
    setCreating(true)
    try {
      const created = await api.createChannel(name.trim(), mode)
      setCreateOpen(false)
      await onChanged()
      notifySuccess(t('dashboard.created'))
      navigate(`/channels/${created.id}`)
    } catch (e) {
      notifyError(t('dashboard.createFailed'), e)
    } finally {
      setCreating(false)
    }
  }

  async function joinChannel() {
    const ref = joinRef.trim()
    if (!ref) return
    setJoining(true)
    try {
      const { channel } = await api.joinChannel(ref)
      setJoinOpen(false)
      await onChanged()
      notifySuccess(t('dashboard.joined'))
      navigate(`/channels/${channel.id}`)
    } catch (e) {
      notifyError(t('dashboard.joinFailed'), e)
    } finally {
      setJoining(false)
    }
  }

  return (
    <>
      <Menu position="bottom-end" width={200} shadow="md">
        <Menu.Target>
          {/* Its own name, distinct from the "New channel" item inside it: two
              controls sharing an accessible name is ambiguous to click and to test. */}
          <ActionIcon variant="subtle" color="gray" size="lg" aria-label={t('chat.addMenu')}>
            <IconPlus size={20} />
          </ActionIcon>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Label>{t('chat.chatsSection')}</Menu.Label>
          <Menu.Item leftSection={<IconMessagePlus size={18} />} onClick={() => setDirectOpen(true)}>
            {t('chat.newChat')}
          </Menu.Item>
          <Menu.Item leftSection={<IconUsersGroup size={18} />} onClick={() => setGroupOpen(true)}>
            {t('chat.newGroup')}
          </Menu.Item>
          <Menu.Divider />
          <Menu.Label>{t('chat.channelsSection')}</Menu.Label>
          <Menu.Item leftSection={<IconPencilPlus size={18} />} onClick={openCreate}>
            {t('dashboard.newChannel')}
          </Menu.Item>
          <Menu.Item leftSection={<IconUserPlus size={18} />} onClick={openJoin}>
            {t('dashboard.addChannel')}
          </Menu.Item>
        </Menu.Dropdown>
      </Menu>

      <NewChatModals
        directOpen={directOpen}
        groupOpen={groupOpen}
        onClose={() => {
          setDirectOpen(false)
          setGroupOpen(false)
        }}
        onChanged={onChanged}
      />

      <ResponsiveModal
        opened={createOpen}
        onClose={() => setCreateOpen(false)}
        title={t('dashboard.newChannel')}
        onEnterTransitionEnd={() => nameRef.current?.focus()}
      >
        <Stack gap="sm">
          <TextInput
            ref={nameRef}
            label={t('dashboard.channelName')}
            placeholder={t('dashboard.channelNamePlaceholder')}
            data-autofocus
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            onKeyDown={(e) => e.key === 'Enter' && createChannel()}
          />
          <div>
            <Text size="sm" fw={500} mb={4}>
              {t('dashboard.subscriptionMode')}
            </Text>
            <SegmentedControl
              fullWidth
              value={mode}
              onChange={(v) => setMode(v as SubscriptionMode)}
              data={[
                { label: t('mode.approval'), value: 'approval' },
                { label: t('mode.open'), value: 'open' },
              ]}
            />
          </div>
          <Group justify="flex-end" mt="sm">
            <Button variant="default" onClick={() => setCreateOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button onClick={createChannel} loading={creating} disabled={!name.trim()}>
              {t('dashboard.createChannel')}
            </Button>
          </Group>
        </Stack>
      </ResponsiveModal>

      <ResponsiveModal
        opened={joinOpen}
        onClose={() => setJoinOpen(false)}
        title={t('dashboard.addChannelTitle')}
        onEnterTransitionEnd={() => joinInputRef.current?.focus()}
      >
        <Stack gap="sm">
          <TextInput
            ref={joinInputRef}
            label={t('dashboard.addChannelRef')}
            placeholder={t('dashboard.addChannelRefPlaceholder')}
            description={t('dashboard.addChannelRefHint')}
            data-autofocus
            value={joinRef}
            onChange={(e) => setJoinRef(e.currentTarget.value)}
            onKeyDown={(e) => e.key === 'Enter' && joinChannel()}
          />
          <Group justify="flex-end" mt="sm">
            <Button variant="default" onClick={() => setJoinOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button onClick={joinChannel} loading={joining} disabled={!joinRef.trim()}>
              {t('dashboard.add')}
            </Button>
          </Group>
        </Stack>
      </ResponsiveModal>
    </>
  )
}
