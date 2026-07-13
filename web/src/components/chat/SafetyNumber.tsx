import { useEffect, useState } from 'react'
import { Alert, Button, Code, Group, Stack, Text } from '@mantine/core'
import { IconAlertTriangle, IconShieldCheck } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../auth/context'
import { mlsSession } from '../../lib/mls'
import { acceptSafetyNumber, checkSafetyNumber, type SafetyState } from '../../lib/safety'
import { ResponsiveModal } from '../ResponsiveModal'

interface SafetyNumberModalProps {
  conversationId: string
  opened: boolean
  onClose: () => void
}

/**
 * Shows the conversation's safety number so two people can compare it out of band.
 *
 * This is the only check that catches a malicious server. Pheme's server hands out
 * the KeyPackages a conversation is built from, so in principle it could hand out
 * its own under someone else's name and sit in the middle of the conversation,
 * reading everything. Nothing in the protocol prevents that. What defeats it is
 * that the number below is derived from the keys actually in the group — so if the
 * server swapped one, the two numbers will not match.
 */
export function SafetyNumberModal({ conversationId, opened, onClose }: SafetyNumberModalProps) {
  const { t } = useTranslation()
  const { userId } = useAuth()
  const [state, setState] = useState<SafetyState | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    if (!opened || !userId) return
    let active = true
    const run = async () => {
      try {
        const session = await mlsSession(userId)
        const current = await session.safetyNumber(conversationId)
        if (active) setState(checkSafetyNumber(conversationId, current))
      } catch {
        // No group yet (encryption still being set up), so there is nothing to show.
        if (active) setFailed(true)
      }
    }
    void run()
    return () => {
      active = false
    }
  }, [opened, conversationId, userId])

  const changed = state?.status === 'changed'

  return (
    <ResponsiveModal opened={opened} onClose={onClose} title={t('safety.title')}>
      <Stack gap="sm">
        {failed && (
          <Text size="sm" c="dimmed">
            {t('safety.notReady')}
          </Text>
        )}

        {changed && (
          <Alert variant="light" color="orange" icon={<IconAlertTriangle size={18} />} p="xs">
            <Text size="xs">{t('safety.changed')}</Text>
          </Alert>
        )}

        {state && (
          <>
            <Text size="sm" c="dimmed">
              {t('safety.description')}
            </Text>
            <Code
              block
              style={{
                fontSize: 'var(--mantine-font-size-md)',
                letterSpacing: '0.08em',
                lineHeight: 1.8,
                textAlign: 'center',
              }}
            >
              {state.number}
            </Code>
            <Group gap="xs" wrap="nowrap" align="flex-start">
              <IconShieldCheck size={18} style={{ flexShrink: 0, marginTop: 2 }} />
              <Text size="xs" c="dimmed">
                {t('safety.howTo')}
              </Text>
            </Group>
            {changed && (
              <Button
                variant="light"
                color="orange"
                onClick={() => {
                  acceptSafetyNumber(conversationId, state.number)
                  onClose()
                }}
              >
                {t('safety.accept')}
              </Button>
            )}
          </>
        )}
      </Stack>
    </ResponsiveModal>
  )
}
