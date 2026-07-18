import { Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import type { MembershipEvent } from '../../lib/mls'
import type { ConversationMember } from '../../lib/types'
import { userLabel } from '../../lib/conversation'

interface MembershipLineProps {
  event: MembershipEvent
  members: ConversationMember[]
  myUserId: string
}

/**
 * The centred line that marks somebody joining or leaving a group.
 *
 * Not a bubble and not attributed to a sender, because nobody sent it: the server writes it when
 * the roster changes. It is the one message in a conversation that is not encrypted — see
 * MEMBERSHIP in lib/mls — which is why it renders straight from the payload with no decryption.
 */
export function MembershipLine({ event, members, myUserId }: MembershipLineProps) {
  const { t } = useTranslation()

  const name = (userId: string): string => {
    if (userId === myUserId) return t('chat.you')
    const member = members.find((m) => m.userId === userId)
    // Someone who has since left is no longer on the roster and cannot be looked up.
    return member?.user ? userLabel(member.user) : `User ${userId.slice(0, 6)}`
  }

  const subject = name(event.userId)
  const by = name(event.actorId)
  const text =
    event.action === 'added'
      ? t('chat.memberAdded', { name: subject, by })
      : event.action === 'removed'
        ? t('chat.memberRemoved', { name: subject, by })
        : t('chat.memberLeft', { name: subject })

  return (
    <Text size="xs" c="dimmed" ta="center" py="xs" px="lg">
      {text}
    </Text>
  )
}
