import { useEffect, useState } from 'react'
import { Alert, Button, Checkbox, Code, CopyButton, Group, PasswordInput, Stack, Text } from '@mantine/core'
import { IconShieldLock } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../auth/context'
import {
  IdentityAlreadySetUpError,
  acceptFreshIdentity,
  backupExists,
  ensureRecoveryBackup,
  hasAcceptedFresh,
  hasLocalKeys,
  loadRecoveryCode,
  regenerateRecoveryCode,
  restoreWithSecret,
} from '../../lib/mls'
import { notifyError, notifySuccess } from '../../lib/notify'
import { ResponsiveModal } from '../ResponsiveModal'

/**
 * Shows the generated recovery code once, and the "save it" acknowledgement — the display of a
 * secret the server can never reproduce. Reused by the first-time gate and the "view code" modal.
 */
function CodeDisplay({ code }: { code: string }) {
  const { t } = useTranslation()
  return (
    <Group gap="sm" wrap="nowrap" align="center">
      <Code
        data-testid="recovery-code"
        style={{ fontSize: '1rem', letterSpacing: '0.05em', flex: 1, padding: '0.5rem 0.75rem' }}
      >
        {code}
      </Code>
      <CopyButton value={code}>
        {({ copied, copy }) => (
          <Button variant="light" size="sm" onClick={copy}>
            {copied ? t('recovery.copied') : t('recovery.copy')}
          </Button>
        )}
      </CopyButton>
    </Group>
  )
}

/**
 * Sets up the encrypted backup automatically the first time a device has keys but no backup, and
 * shows the fresh recovery code ONCE. Mounted on the chat shell. After this, auto-backup keeps the
 * server copy current (the code is remembered in memory this session).
 *
 * Not dismissible until the user confirms they saved it — the code cannot be shown again on a device
 * that did not generate it, so losing it here loses recoverable history.
 */
export function RecoveryCodeGate() {
  const { userId } = useAuth()
  const { t } = useTranslation()
  const [code, setCode] = useState<string | null>(null)
  const [acked, setAcked] = useState(false)

  useEffect(() => {
    if (!userId) return
    let active = true
    // ensureRecoveryBackup is a no-op when a backup already exists, so this runs at most once per
    // device — on the first open after encryption is set up (including for existing users on upgrade).
    ensureRecoveryBackup(userId)
      .then((generated) => {
        if (!active || !generated) return
        // The backup is done regardless; the E2E suite suppresses only the forced one-time DISPLAY
        // so it does not block every test behind this modal. The dedicated recovery test does not
        // set the flag and exercises the real prompt.
        if ((window as { __phemeSkipRecoveryPrompt?: boolean }).__phemeSkipRecoveryPrompt) return
        setCode(generated)
      })
      .catch(() => {
        // No keys yet, or the network is down; the next open tries again. Never blocks the chat.
      })
    return () => {
      active = false
    }
  }, [userId])

  return (
    <ResponsiveModal opened={code !== null} onClose={() => {}} title={t('recovery.setupTitle')}>
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          {t('recovery.setupDescription')}
        </Text>
        {code && <CodeDisplay code={code} />}
        <Alert variant="light" color="yellow" icon={<IconShieldLock size={18} />} p="xs">
          <Text size="xs">{t('recovery.setupWarning')}</Text>
        </Alert>
        <Checkbox
          checked={acked}
          onChange={(e) => setAcked(e.currentTarget.checked)}
          label={t('recovery.saved')}
        />
        <Button disabled={!acked} onClick={() => setCode(null)}>
          {t('recovery.done')}
        </Button>
      </Stack>
    </ResponsiveModal>
  )
}

interface RecoveryCodeModalProps {
  opened: boolean
  onClose: () => void
}

/**
 * View the recovery code again on the device that generated it, or generate a new one. Opened from
 * the sidebar menu. A restored device holds no local code (it was set elsewhere) — there we explain
 * that and offer to generate a fresh one, which re-seals the backup and invalidates the old code.
 */
export function RecoveryCodeModal({ opened, onClose }: RecoveryCodeModalProps) {
  const { t } = useTranslation()
  const { userId } = useAuth()
  const [code, setCode] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!opened) return
    let active = true
    loadRecoveryCode().then((c) => active && setCode(c))
    return () => {
      active = false
    }
  }, [opened])

  async function regenerate() {
    if (!userId || busy) return
    if (!window.confirm(t('recovery.regenerateConfirm'))) return
    setBusy(true)
    try {
      const next = await regenerateRecoveryCode(userId)
      setCode(next)
      notifySuccess(t('recovery.regenerated'))
    } catch (e) {
      notifyError(t('recovery.regenerateFailed'), e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <ResponsiveModal opened={opened} onClose={onClose} title={t('recovery.viewTitle')}>
      <Stack gap="sm">
        {code ? (
          <>
            <Text size="sm" c="dimmed">
              {t('recovery.viewDescription')}
            </Text>
            <CodeDisplay code={code} />
          </>
        ) : (
          <Alert variant="light" color="blue" icon={<IconShieldLock size={18} />} p="xs">
            <Text size="xs">{t('recovery.notOnThisDevice')}</Text>
          </Alert>
        )}
        <Button variant="subtle" color="gray" onClick={regenerate} loading={busy}>
          {t('recovery.regenerate')}
        </Button>
      </Stack>
    </ResponsiveModal>
  )
}

/**
 * Shown automatically when this device has no local keys but the server holds a backup — a fresh
 * device, or after IndexedDB was evicted. Recovering here brings the history back and comes up under
 * this device's OWN identity (restoreWithSecret → restoreKeys, which never clones the backed-up
 * device). Accepts a recovery code (typed loosely) or a legacy passphrase.
 */
export function KeyRestoreGate() {
  const { t } = useTranslation()
  const { userId } = useAuth()
  const [needed, setNeeded] = useState(false)
  const [secret, setSecret] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let active = true
    const run = async () => {
      try {
        if (await hasLocalKeys()) return
        // The user already chose to start fresh here; do not nag to restore a backup they declined.
        if (await hasAcceptedFresh()) return
        const exists = await backupExists()
        if (!exists || !active) return
        // Test hook: a fresh independent device (the multi-device crypto suites) starts over rather
        // than restoring. Production never sets this, so a real second device sees the prompt.
        if ((window as { __phemeAutoStartFresh?: boolean }).__phemeAutoStartFresh) {
          await startFresh()
          return
        }
        setNeeded(true)
      } catch {
        // Could not reach the server to find out. Say nothing rather than guess: the session
        // bootstrap refuses to mint an identity in this state anyway.
      }
    }
    void run()
    return () => {
      active = false
    }
  }, [])

  async function restore() {
    if (busy || secret.trim().length === 0 || !userId) return
    setBusy(true)
    try {
      const ok = await restoreWithSecret(userId, secret.trim())
      if (!ok) {
        setNeeded(false) // the backup is gone from the server; nothing to restore
        return
      }
      window.location.reload()
    } catch (e) {
      if (e instanceof IdentityAlreadySetUpError) {
        notifyError(t('backup.alreadySetUp'), null)
        window.location.reload()
        return
      }
      notifyError(t('backup.wrongPassphrase'), null)
    } finally {
      setBusy(false)
    }
  }

  async function startFresh() {
    await acceptFreshIdentity()
    window.location.reload()
  }

  return (
    <ResponsiveModal opened={needed} onClose={() => {}} title={t('recovery.restoreTitle')}>
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          {t('recovery.restoreDescription')}
        </Text>
        <PasswordInput
          data-autofocus
          label={t('recovery.codeLabel')}
          placeholder={t('recovery.codePlaceholder')}
          description={t('recovery.codeHint')}
          value={secret}
          onChange={(e) => setSecret(e.currentTarget.value)}
          onKeyDown={(e) => e.key === 'Enter' && restore()}
        />
        <Button onClick={restore} loading={busy} disabled={secret.trim().length === 0}>
          {t('backup.restore')}
        </Button>
        <Button variant="subtle" color="gray" onClick={startFresh}>
          {t('backup.skip')}
        </Button>
      </Stack>
    </ResponsiveModal>
  )
}
