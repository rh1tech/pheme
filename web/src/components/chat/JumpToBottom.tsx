import { Badge, ActionIcon } from '@mantine/core'
import { IconArrowDown } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'

interface JumpToBottomProps {
  visible: boolean
  /** Messages that arrived while the reader was scrolled up. */
  count: number
  onClick: () => void
}

export function JumpToBottom({ visible, count, onClick }: JumpToBottomProps) {
  const { t } = useTranslation()
  return (
    <div className="pheme-jump" data-visible={visible}>
      <ActionIcon
        aria-label={t('chat.jumpToBottom')}
        onClick={onClick}
        size={44}
        radius="xl"
        variant="default"
        // Hidden from the tab order while it is invisible: a pill the reader
        // cannot see should not be focusable.
        tabIndex={visible ? 0 : -1}
        style={{ boxShadow: 'var(--mantine-shadow-md)' }}
      >
        <IconArrowDown size={20} />
      </ActionIcon>
      {count > 0 && (
        <Badge
          size="sm"
          circle
          style={{ position: 'absolute', top: -4, right: -4, pointerEvents: 'none' }}
        >
          {count}
        </Badge>
      )}
    </div>
  )
}
