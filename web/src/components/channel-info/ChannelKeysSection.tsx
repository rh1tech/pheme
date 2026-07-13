import { useEffect, useState } from 'react'
import {
  ActionIcon,
  Button,
  Card,
  Code,
  CopyButton,
  Group,
  Modal,
  Stack,
  Table,
  Text,
  Tooltip,
} from '@mantine/core'
import { IconTrash } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { api } from '../../lib/api'
import { notifyError, notifySuccess } from '../../lib/notify'
import type { ApiKey, CreatedKey } from '../../lib/types'

interface ChannelKeysSectionProps {
  channelId: string
}

/** Owner-only: the API keys websites use to trigger this channel. */
export function ChannelKeysSection({ channelId }: ChannelKeysSectionProps) {
  const { t } = useTranslation()
  const [keys, setKeys] = useState<ApiKey[]>([])
  const [created, setCreated] = useState<CreatedKey | null>(null)

  async function reload() {
    try {
      setKeys(await api.listKeys(channelId))
    } catch {
      // A key list that fails to load is not worth interrupting the panel for.
    }
  }

  useEffect(() => {
    let active = true
    api
      .listKeys(channelId)
      .then((ks) => active && setKeys(ks))
      .catch(() => undefined)
    return () => {
      active = false
    }
  }, [channelId])

  async function createKey() {
    try {
      setCreated(await api.createKey(channelId))
      await reload()
    } catch (e) {
      notifyError(t('channel.keyFailed'), e)
    }
  }

  async function revokeKey(keyId: string) {
    try {
      await api.revokeKey(channelId, keyId)
      await reload()
      notifySuccess(t('channel.keyRevoked'))
    } catch (e) {
      notifyError(t('channel.revokeFailed'), e)
    }
  }

  const active = keys.filter((k) => !k.revokedAt)

  return (
    <>
      <Modal
        opened={created !== null}
        onClose={() => setCreated(null)}
        title={t('channel.keyCreatedTitle')}
      >
        <Stack>
          <Text size="sm" c="dimmed">
            {t('channel.keyShownOnce')}
          </Text>
          <Code block>{created?.key}</Code>
          <Group justify="flex-end">
            <CopyButton value={created?.key ?? ''}>
              {({ copied, copy }) => (
                <Button onClick={copy}>{copied ? t('common.copied') : t('channel.copyKey')}</Button>
              )}
            </CopyButton>
          </Group>
        </Stack>
      </Modal>

      <Card withBorder padding="md">
        <Group justify="space-between" mb="sm">
          <Text fw={600}>{t('channel.tabs.keys')}</Text>
          <Button size="xs" variant="light" onClick={createKey}>
            {t('channel.createKey')}
          </Button>
        </Group>

        {active.length === 0 ? (
          <Text c="dimmed" size="sm">
            {t('channel.noKeys')}
          </Text>
        ) : (
          <Table verticalSpacing="xs">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t('channel.colPrefix')}</Table.Th>
                <Table.Th>{t('channel.colCreated')}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {active.map((k) => (
                <Table.Tr key={k.id}>
                  <Table.Td>
                    <Code>{k.prefix}…</Code>
                  </Table.Td>
                  <Table.Td>{new Date(k.createdAt).toLocaleDateString()}</Table.Td>
                  <Table.Td align="right">
                    <Tooltip label={t('channel.revoke')}>
                      <ActionIcon
                        color="red"
                        variant="subtle"
                        aria-label={t('channel.revoke')}
                        onClick={() => revokeKey(k.id)}
                      >
                        <IconTrash size={16} />
                      </ActionIcon>
                    </Tooltip>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </Card>
    </>
  )
}
