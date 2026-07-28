import { useState } from 'react'
import { Button, Card, Code, CopyButton, Group, Modal, Stack, Text } from '@mantine/core'
import { QRCodeSVG } from 'qrcode.react'
import { useTranslation } from 'react-i18next'
import { apiBase } from '../lib/server'

/**
 * Which server this browser is signed in to, and how to hand it to somebody else.
 *
 * Read-only on purpose, and it is not an oversight that there is no Save. Everything a session
 * holds — the tokens, the push subscription, the MLS identity, the keys in IndexedDB — belongs to
 * the server it was made on, and none of it survives being repointed at another one. Changing the
 * server is signing out and signing back in, which is where the editable field lives.
 *
 * The QR is the part that earns its keep. A self-hosted server lives at an unlisted path prefix —
 * `https://host.example/a7f3c91e4b2d` — long, case-sensitive and meaningless, which is exactly the
 * kind of string somebody mistypes once and gives up on. The person who already has it working
 * should be able to show it rather than dictate it.
 */
export function ServerSection() {
  const { t } = useTranslation()
  const [sharing, setSharing] = useState(false)
  const url = apiBase()

  if (url === '') return null

  return (
    <>
      <Card withBorder padding="md">
        <Stack gap="xs">
          <Group justify="space-between" align="flex-start" wrap="nowrap">
            <Stack gap={2}>
              <Text fw={600}>{t('settings.serverTitle')}</Text>
              <Text size="sm" c="dimmed">
                {t('settings.serverHint')}
              </Text>
            </Stack>
            <Button variant="light" onClick={() => setSharing(true)}>
              {t('settings.serverShare')}
            </Button>
          </Group>
          {/* Wrapping, not truncating: the unlisted prefix is the part somebody needs to read, and
              it is at the END of the address. */}
          <Code style={{ wordBreak: 'break-all' }}>{url}</Code>
        </Stack>
      </Card>

      <Modal
        opened={sharing}
        onClose={() => setSharing(false)}
        title={t('settings.serverShare')}
        centered
      >
        <Stack gap="sm" align="center">
          <Text size="sm" c="dimmed" ta="center">
            {t('settings.serverShareHint')}
          </Text>

          {/* A light quiet zone whatever the theme: a QR is read by a camera, not by a person, and
              inverted codes are a coin toss across scanners. */}
          <div style={{ background: '#fff', padding: 12, borderRadius: 8 }}>
            <QRCodeSVG value={url} size={190} />
          </div>

          {/* The address in full as well. A QR is no use over a phone call. */}
          <Code style={{ wordBreak: 'break-all', textAlign: 'center' }}>{url}</Code>

          <CopyButton value={url}>
            {({ copied, copy }) => (
              <Button variant="light" onClick={copy} fullWidth>
                {copied ? t('common.copied') : t('common.copy')}
              </Button>
            )}
          </CopyButton>
        </Stack>
      </Modal>
    </>
  )
}
