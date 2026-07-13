import { useRef, useState } from 'react'
import { Button, Group, Overlay, Stack } from '@mantine/core'
import { IconCamera } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { notifyError, notifySuccess } from '../../lib/notify'
import { ChannelAvatar } from '../chat/ChannelAvatar'
import type { Channel } from '../../lib/types'

// Matches the server's limit (internal/channel/notify_input.go).
const MAX_AVATAR_BYTES = 10 * 1024 * 1024
const SIZE = 96

interface ChannelAvatarPickerProps {
  channel: Channel
  /** Owners set the picture; everyone else just looks at it. */
  canEdit: boolean
  onChanged: (next: Channel) => void
}

/** The channel's picture, with an upload/remove control for its owner. */
export function ChannelAvatarPicker({ channel, canEdit, onChanged }: ChannelAvatarPickerProps) {
  const { t } = useTranslation()
  const fileRef = useRef<HTMLInputElement>(null)
  const [busy, setBusy] = useState(false)

  async function upload(file: File) {
    if (file.size > MAX_AVATAR_BYTES) {
      notifyError(t('channel.imageTooLarge', { name: file.name }))
      return
    }
    setBusy(true)
    try {
      onChanged(await api.uploadChannelAvatar(channel.id, file))
      notifySuccess(t('channel.avatarUpdated'))
    } catch (e) {
      notifyError(t('channel.avatarFailed'), e)
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    setBusy(true)
    try {
      onChanged(await api.deleteChannelAvatar(channel.id))
      notifySuccess(t('channel.avatarRemoved'))
    } catch (e) {
      notifyError(t('channel.avatarFailed'), e)
    } finally {
      setBusy(false)
    }
  }

  if (!canEdit) {
    return <ChannelAvatar id={channel.id} name={channel.name} avatarId={channel.avatarId} size={SIZE} />
  }

  return (
    <Stack align="center" gap="xs">
      <button
        type="button"
        className="pheme-avatar-edit"
        aria-label={t('channel.changeAvatar')}
        disabled={busy}
        onClick={() => fileRef.current?.click()}
        style={{ width: SIZE, height: SIZE }}
      >
        <ChannelAvatar
          id={channel.id}
          name={channel.name}
          avatarId={channel.avatarId}
          size={SIZE}
        />
        <Overlay className="pheme-avatar-edit-veil" radius={9999} backgroundOpacity={0.55} color="#000">
          <IconCamera size={24} color="#fff" />
        </Overlay>
      </button>

      <input
        ref={fileRef}
        type="file"
        accept="image/*"
        hidden
        onChange={(e) => {
          const file = e.currentTarget.files?.[0]
          e.currentTarget.value = ''
          if (file) void upload(file)
        }}
      />

      {channel.avatarId && (
        <Group gap="xs">
          <Button size="compact-xs" variant="subtle" color="red" loading={busy} onClick={remove}>
            {t('channel.removeAvatar')}
          </Button>
        </Group>
      )}
    </Stack>
  )
}
