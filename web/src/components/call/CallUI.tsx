import { ActionIcon, Button, Group, Modal, Stack, Text } from '@mantine/core'
import { IconPhone, IconPhoneOff } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { useCalls } from './context'
import type { CallEndReason, CallStatus } from '../../lib/call'
import './call.css'

/**
 * The two things a call ever shows: somebody is calling you, and you are on a call.
 *
 * Both are mounted above the routes, so a call survives navigating between conversations — you
 * can look something up mid-call without dropping it.
 */
export function CallUI() {
  return (
    <>
      <IncomingCall />
      <InCall />
    </>
  )
}

function IncomingCall() {
  const { incoming, answer, decline } = useCalls()
  const { t } = useTranslation()

  return (
    <Modal
      opened={incoming !== null}
      onClose={() => void decline()}
      withCloseButton={false}
      centered
      title={t('call.incoming')}
    >
      {/* The testid goes on real content, not on the Modal root: Mantine's root is a
          zero-size portal wrapper, so an assertion on its visibility is false even when the
          dialog is open. */}
      <Stack gap="lg" data-testid="incoming-call">
        <Text size="sm" c="dimmed">
          {t('call.incomingBody')}
        </Text>
        <Group justify="center" gap="xl">
          <Stack gap={6} align="center">
            <ActionIcon
              size={56}
              radius="xl"
              color="red"
              variant="filled"
              aria-label={t('call.decline')}
              data-testid="decline-call"
              onClick={() => void decline()}
            >
              <IconPhoneOff size={26} />
            </ActionIcon>
            <Text size="xs" c="dimmed">
              {t('call.decline')}
            </Text>
          </Stack>
          <Stack gap={6} align="center">
            <ActionIcon
              size={56}
              radius="xl"
              color="green"
              variant="filled"
              aria-label={t('call.answer')}
              data-testid="answer-call"
              onClick={() => void answer()}
              className="pheme-call-ring"
            >
              <IconPhone size={26} />
            </ActionIcon>
            <Text size="xs" c="dimmed">
              {t('call.answer')}
            </Text>
          </Stack>
        </Group>
      </Stack>
    </Modal>
  )
}

function InCall() {
  const { call, hangUp, dismiss } = useCalls()
  const { t } = useTranslation()
  if (!call) return null

  const ended = call.status === 'ended'

  return (
    <div className="pheme-call-bar" data-testid="call-bar" data-status={call.status}>
      <Group justify="space-between" wrap="nowrap" gap="sm">
        <Stack gap={0} style={{ minWidth: 0 }}>
          <Text size="sm" fw={600} truncate data-testid="call-status">
            {statusLabel(call.status, call.reason, t)}
          </Text>
          {call.status === 'connected' && (
            <Text size="xs" c="dimmed">
              {t('call.encrypted')}
            </Text>
          )}
        </Stack>
        {ended ? (
          <Button size="xs" variant="subtle" onClick={dismiss}>
            {t('call.dismiss')}
          </Button>
        ) : (
          <ActionIcon
            size="lg"
            radius="xl"
            color="red"
            variant="filled"
            aria-label={t('call.hangUp')}
            data-testid="hang-up"
            onClick={() => void hangUp()}
          >
            <IconPhoneOff size={18} />
          </ActionIcon>
        )}
      </Group>
    </div>
  )
}

/** What the bar says. An ended call says WHY, which is the whole reason it lingers. */
function statusLabel(
  status: CallStatus,
  reason: CallEndReason | undefined,
  t: (k: string) => string,
): string {
  if (status !== 'ended') {
    return t(`call.status.${status}`)
  }
  switch (reason) {
    case 'declined':
      return t('call.ended.declined')
    case 'busy':
      return t('call.ended.busy')
    case 'unanswered':
      return t('call.ended.unanswered')
    case 'answered-elsewhere':
      return t('call.ended.answeredElsewhere')
    case 'failed':
      return t('call.ended.failed')
    case 'out-of-sync':
      return t('call.ended.outOfSync')
    default:
      return t('call.ended.hungUp')
  }
}
