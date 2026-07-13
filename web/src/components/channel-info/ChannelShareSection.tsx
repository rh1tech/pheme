import { Button, Card, Code, CopyButton, Group, Stack, Text } from '@mantine/core'
import { QRCodeSVG } from 'qrcode.react'
import { useTranslation } from 'react-i18next'
import type { Channel } from '../../lib/types'

interface ChannelShareSectionProps {
  channel: Channel
}

/** Owner-only: the QR and identifiers people use to join the channel. */
export function ChannelShareSection({ channel }: ChannelShareSectionProps) {
  const { t } = useTranslation()
  // The deep link the QR encodes; the /join route resolves the ref.
  const shareRef = channel.alias || channel.publicId
  const shareUrl = `${window.location.origin}/join?ref=${encodeURIComponent(shareRef)}`

  return (
    <Card withBorder padding="md">
      <Stack gap="sm" align="center">
        <Stack gap={2} align="center">
          <Text fw={600}>{t('channel.shareTitle')}</Text>
          <Text size="sm" c="dimmed" ta="center">
            {t('channel.shareDescription')}
          </Text>
        </Stack>

        {/* The QR needs a light quiet zone to scan reliably, in either theme. */}
        <div style={{ background: '#fff', padding: 12, borderRadius: 8 }}>
          <QRCodeSVG value={shareUrl} size={168} />
        </div>

        <Stack gap="xs" align="center">
          <Group gap="xs">
            <Text size="sm" c="dimmed">
              {t('channel.triggerId')}
            </Text>
            <Code>{channel.publicId}</Code>
            <CopyButton value={channel.publicId}>
              {({ copied, copy }) => (
                <Button size="compact-xs" variant="subtle" onClick={copy}>
                  {copied ? t('common.copied') : t('common.copy')}
                </Button>
              )}
            </CopyButton>
          </Group>

          {channel.alias && (
            <Group gap="xs">
              <Text size="sm" c="dimmed">
                {t('channel.phetag')}
              </Text>
              <Code>@{channel.alias}</Code>
              <CopyButton value={channel.alias}>
                {({ copied, copy }) => (
                  <Button size="compact-xs" variant="subtle" onClick={copy}>
                    {copied ? t('common.copied') : t('common.copy')}
                  </Button>
                )}
              </CopyButton>
            </Group>
          )}
        </Stack>
      </Stack>
    </Card>
  )
}
