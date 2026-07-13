import { Menu } from '@mantine/core'
import {
  IconCopy,
  IconLink,
  IconMessageCircle,
  IconTrash,
} from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { notifySuccess } from '../../lib/notify'
import type { Message } from '../../lib/types'

export interface MenuTarget {
  message: Message
  x: number
  y: number
}

interface MessageMenuProps {
  target: MenuTarget
  channelId: string
  /** Owners and channel-admins may delete what the channel published. */
  canModerate: boolean
  onClose: () => void
  onOpenDiscussion: (messageId: string) => void
  onDelete: (message: Message) => void
}

/** What a message can do, on right-click or long-press. */
export function MessageMenu({
  target,
  channelId,
  canModerate,
  onClose,
  onOpenDiscussion,
  onDelete,
}: MessageMenuProps) {
  const { t } = useTranslation()
  const { message, x, y } = target

  const text = [message.title, message.body].filter(Boolean).join('\n')
  const link = `${window.location.origin}/channels/${channelId}/messages/${message.id}`

  async function copy(value: string, done: string) {
    try {
      await navigator.clipboard.writeText(value)
      notifySuccess(done)
    } catch {
      // Clipboard access is denied outside a secure context, and there is nothing
      // useful to say about it — the menu simply closes.
    }
    onClose()
  }

  return (
    <Menu
      opened
      onClose={onClose}
      withinPortal
      position="bottom-start"
      shadow="md"
      width={220}
    >
      {/* An empty, zero-size target placed where the press landed: the menu opens
          at the message the reader actually touched, not at a fixed anchor. */}
      <Menu.Target>
        <span
          style={{ position: 'fixed', left: x, top: y, width: 0, height: 0 }}
          aria-hidden
        />
      </Menu.Target>
      <Menu.Dropdown data-testid="message-menu">
        {text && (
          <Menu.Item
            leftSection={<IconCopy size={16} />}
            onClick={() => copy(text, t('channel.copiedText'))}
          >
            {t('channel.copyText')}
          </Menu.Item>
        )}
        <Menu.Item
          leftSection={<IconLink size={16} />}
          onClick={() => copy(link, t('channel.copiedLink'))}
        >
          {t('channel.copyLink')}
        </Menu.Item>
        {message.commentsAllowed && (
          <Menu.Item
            leftSection={<IconMessageCircle size={16} />}
            onClick={() => {
              onOpenDiscussion(message.id)
              onClose()
            }}
          >
            {t('channel.discussion')}
          </Menu.Item>
        )}
        {canModerate && (
          <>
            <Menu.Divider />
            <Menu.Item
              color="red"
              leftSection={<IconTrash size={16} />}
              onClick={() => {
                onDelete(message)
                onClose()
              }}
            >
              {t('common.delete')}
            </Menu.Item>
          </>
        )}
      </Menu.Dropdown>
    </Menu>
  )
}
