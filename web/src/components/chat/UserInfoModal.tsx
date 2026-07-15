import { ActionIcon, CopyButton, Group, Stack, Text, Tooltip } from '@mantine/core'
import { IconCheck, IconCopy } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { ChannelAvatar } from './ChannelAvatar'
import { userLabel } from '../../lib/conversation'
import { notifySuccess } from '../../lib/notify'
import { ResponsiveModal } from '../ResponsiveModal'
import type { PublicUser } from '../../lib/types'

interface UserInfoModalProps {
  user: PublicUser | null
  opened: boolean
  onClose: () => void
}

/**
 * A single contact's public profile — everything this app knows about another
 * user: their picture, their name, their @username, and their Pheme ID. There is
 * no richer public shape (bio, phone) for other people; PublicUser is all the
 * server exposes, so this shows exactly that and nothing it cannot honour.
 */
export function UserInfoModal({ user, opened, onClose }: UserInfoModalProps) {
  const { t } = useTranslation()
  if (!user) return null

  const label = userLabel(user)

  return (
    <ResponsiveModal opened={opened} onClose={onClose} title={t('chat.userInfo')}>
      <Stack align="center" gap="xs" pb="sm">
        <ChannelAvatar id={user.id} name={label} avatarId={user.avatarId} size={96} />
        <Text fw={600} size="lg" ta="center">
          {label}
        </Text>
        {user.username ? (
          <Text c="dimmed" size="sm">
            @{user.username}
          </Text>
        ) : (
          <Text c="dimmed" size="sm" fs="italic">
            {t('chat.noUsername')}
          </Text>
        )}
      </Stack>

      <Group
        justify="space-between"
        wrap="nowrap"
        gap="xs"
        p="xs"
        style={{
          background: 'var(--pheme-field-bg)',
          borderRadius: 'var(--mantine-radius-md)',
        }}
      >
        <Stack gap={0} style={{ minWidth: 0 }}>
          <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
            {t('chat.phemeId')}
          </Text>
          <Text size="sm" ff="monospace" style={{ wordBreak: 'break-all' }}>
            {user.id}
          </Text>
        </Stack>
        <CopyButton value={user.id}>
          {({ copied, copy }) => (
            <Tooltip label={copied ? t('common.copied') : t('common.copy')} withArrow>
              <ActionIcon
                variant="subtle"
                color={copied ? 'teal' : 'gray'}
                aria-label={t('common.copy')}
                onClick={() => {
                  copy()
                  notifySuccess(t('chat.phemeIdCopied'))
                }}
              >
                {copied ? <IconCheck size={18} /> : <IconCopy size={18} />}
              </ActionIcon>
            </Tooltip>
          )}
        </CopyButton>
      </Group>
    </ResponsiveModal>
  )
}
