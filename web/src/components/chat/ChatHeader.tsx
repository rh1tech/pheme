import { useEffect, useRef } from 'react'
import { ActionIcon, Group, Stack, Text, TextInput, Tooltip } from '@mantine/core'
import {
  IconArrowLeft,
  IconChevronDown,
  IconChevronUp,
  IconDotsVertical,
  IconSearch,
  IconX,
} from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { ChannelAvatar } from './ChannelAvatar'
import type { Channel } from '../../lib/types'

interface ChatHeaderProps {
  channel: Channel | null
  channelId: string
  /** Name and avatar to show before the channel fetch lands (from the list). */
  hintName?: string
  hintAvatarId?: string
  /** The header swaps into a search field, as Telegram's does. */
  searching: boolean
  search: string
  onSearchChange: (value: string) => void
  onSearchSubmit: () => void
  onSearchOpen: () => void
  onSearchClose: () => void
  /** How many messages the current query matched, and which one is showing. */
  hitCount: number
  hitIndex: number
  onPrevHit: () => void
  onNextHit: () => void
  onToggleInfo: () => void
}

export function ChatHeader({
  channel,
  channelId,
  hintName,
  hintAvatarId,
  searching,
  search,
  onSearchChange,
  onSearchSubmit,
  onSearchOpen,
  onSearchClose,
  hitCount,
  hitIndex,
  onPrevHit,
  onNextHit,
  onToggleInfo,
}: ChatHeaderProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const searchRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (searching) searchRef.current?.focus()
  }, [searching])

  const name = channel?.name ?? hintName ?? t('channel.fallbackName')

  return (
    <header className="pheme-chat-header" data-testid="chat-header">
      <Group gap="sm" wrap="nowrap">
        <ActionIcon
          hiddenFrom="sm"
          variant="subtle"
          color="gray"
          aria-label={t('chat.back')}
          onClick={() => navigate('/')}
        >
          <IconArrowLeft size={20} />
        </ActionIcon>

        {searching ? (
          <TextInput
            ref={searchRef}
            data-testid="chat-search"
            aria-label={t('channel.searchMessages')}
            placeholder={t('channel.searchPlaceholder')}
            value={search}
            onChange={(e) => onSearchChange(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') onSearchSubmit()
              if (e.key === 'Escape') onSearchClose()
            }}
            leftSection={<IconSearch size={16} stroke={1.8} />}
            style={{ flex: 1 }}
            // See ChatSidebar: a bare input floats iOS's AutoFill dropdown, clipped
            // above the keyboard. Declaring it a search field suppresses it.
            type="search"
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="none"
            spellCheck={false}
            enterKeyHint="search"
          />
        ) : (
          <>
            <ChannelAvatar
              id={channelId}
              name={name}
              avatarId={channel?.avatarId ?? hintAvatarId}
              size={38}
              label={t('channel.viewInfo', { name })}
              onClick={onToggleInfo}
            />
            <Stack gap={0} style={{ flex: 1, minWidth: 0 }}>
              <Text fw={600} size="sm" truncate>
                {name}
              </Text>
              {channel?.alias && (
                <Text size="xs" c="dimmed" truncate>
                  @{channel.alias}
                </Text>
              )}
            </Stack>
          </>
        )}

        <Group gap={4} wrap="nowrap">
          {searching && hitCount > 0 && (
            <>
              <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap' }}>
                {hitIndex + 1}/{hitCount}
              </Text>
              <ActionIcon
                variant="subtle"
                color="gray"
                aria-label={t('channel.olderHit')}
                onClick={onPrevHit}
              >
                <IconChevronDown size={18} />
              </ActionIcon>
              <ActionIcon
                variant="subtle"
                color="gray"
                aria-label={t('channel.newerHit')}
                onClick={onNextHit}
              >
                <IconChevronUp size={18} />
              </ActionIcon>
            </>
          )}
          {searching ? (
            <ActionIcon
              variant="subtle"
              color="gray"
              aria-label={t('channel.closeSearch')}
              onClick={onSearchClose}
            >
              <IconX size={20} />
            </ActionIcon>
          ) : (
            <Tooltip label={t('channel.searchMessages')} withArrow>
              <ActionIcon
                variant="subtle"
                color="gray"
                aria-label={t('channel.searchMessages')}
                onClick={onSearchOpen}
              >
                <IconSearch size={20} />
              </ActionIcon>
            </Tooltip>
          )}
          <Tooltip label={t('channel.info')} withArrow>
            <ActionIcon
              variant="subtle"
              color="gray"
              aria-label={t('channel.info')}
              onClick={onToggleInfo}
            >
              <IconDotsVertical size={20} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>
    </header>
  )
}
