import { useEffect, useState } from 'react'
import { ActionIcon, Button, Group, Menu, Modal, Stack, Text } from '@mantine/core'
import {
  IconMicrophone,
  IconMicrophoneOff,
  IconPhone,
  IconPhoneOff,
  IconSettings,
} from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { useCalls } from './context'
import { listAudioDevices } from '../../lib/call'
import type { AudioDevice, CallEndReason, CallStatus } from '../../lib/call'
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
  const { call, hangUp, dismiss, setMuted, setInputDevice, setOutputDevice } = useCalls()
  const { t } = useTranslation()
  const [devices, setDevices] = useState<{ inputs: AudioDevice[]; outputs: AudioDevice[] }>({
    inputs: [],
    outputs: [],
  })

  const live = call?.status === 'connected' || call?.status === 'connecting'

  // Devices are listed only once a call is up, and re-listed when one is plugged in or pulled
  // out. Before the microphone permission is granted the labels come back empty, so a list
  // gathered any earlier would offer a choice between "audioinput 1" and "audioinput 2".
  useEffect(() => {
    if (!live) return
    const refresh = () => void listAudioDevices().then(setDevices)
    refresh()
    navigator.mediaDevices?.addEventListener('devicechange', refresh)
    return () => navigator.mediaDevices?.removeEventListener('devicechange', refresh)
  }, [live])

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
              {call.muted ? t('call.muted') : t('call.encrypted')}
            </Text>
          )}
        </Stack>

        {ended ? (
          <Button size="xs" variant="subtle" onClick={dismiss}>
            {t('call.dismiss')}
          </Button>
        ) : (
          <Group gap="xs" wrap="nowrap">
            <ActionIcon
              size="lg"
              radius="xl"
              variant={call.muted ? 'filled' : 'default'}
              color={call.muted ? 'red' : undefined}
              aria-label={call.muted ? t('call.unmute') : t('call.mute')}
              aria-pressed={call.muted}
              data-testid="toggle-mute"
              onClick={() => setMuted(!call.muted)}
            >
              {call.muted ? <IconMicrophoneOff size={18} /> : <IconMicrophone size={18} />}
            </ActionIcon>

            {/* Only worth opening if there is actually a choice inside. A machine with one
                microphone and no way to redirect the output has nothing to offer here — and on
                an iPhone there is nothing to offer at all, because Safari implements no
                setSinkId and the web cannot move a call between the earpiece and the
                loudspeaker. Showing the menu anyway would be a control that does nothing. */}
            {(devices.inputs.length > 1 || devices.outputs.length > 1) && (
              <Menu position="top-end" withinPortal>
                <Menu.Target>
                  <ActionIcon
                    size="lg"
                    radius="xl"
                    variant="default"
                    aria-label={t('call.audioSettings')}
                    data-testid="call-devices"
                  >
                    <IconSettings size={18} />
                  </ActionIcon>
                </Menu.Target>
                <Menu.Dropdown>
                  {devices.inputs.length > 1 && (
                    <>
                      <Menu.Label>{t('call.microphone')}</Menu.Label>
                      {devices.inputs.map((d) => (
                        <Menu.Item
                          key={d.deviceId}
                          onClick={() => void setInputDevice(d.deviceId)}
                        >
                          {d.label}
                        </Menu.Item>
                      ))}
                    </>
                  )}
                  {devices.outputs.length > 1 && (
                    <>
                      <Menu.Label>{t('call.speaker')}</Menu.Label>
                      {devices.outputs.map((d) => (
                        <Menu.Item
                          key={d.deviceId}
                          onClick={() => void setOutputDevice(d.deviceId)}
                        >
                          {d.label}
                        </Menu.Item>
                      ))}
                    </>
                  )}
                </Menu.Dropdown>
              </Menu>
            )}

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
          </Group>
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
