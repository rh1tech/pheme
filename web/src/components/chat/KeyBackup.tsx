import { useEffect, useState } from 'react'
import { Alert, Button, PasswordInput, Stack, Text } from '@mantine/core'
import { IconShieldLock } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../auth/context'
import { backupExists, backupKeys, hasLocalKeys, restoreKeys } from '../../lib/mls'
import { notifyError, notifySuccess } from '../../lib/notify'
import { ResponsiveModal } from '../ResponsiveModal'

const MIN_PASSPHRASE = 8

interface KeyBackupModalProps {
  opened: boolean
  onClose: () => void
}

/**
 * Set up (or refresh) the encrypted key backup. The passphrase seals the device's
 * MLS state client-side; the server only ever stores the resulting ciphertext, so
 * losing the passphrase loses the recoverable history — that trade is stated here.
 */
export function KeyBackupModal({ opened, onClose }: KeyBackupModalProps) {
  const { t } = useTranslation()
  const { userId } = useAuth()
  const [passphrase, setPassphrase] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)

  const tooShort = passphrase.length < MIN_PASSPHRASE
  const mismatch = confirm.length > 0 && confirm !== passphrase
  const canSave = !tooShort && !mismatch && confirm === passphrase

  async function save() {
    if (!canSave || busy || !userId) return
    setBusy(true)
    try {
      await backupKeys(userId, passphrase)
      notifySuccess(t('backup.saved'))
      setPassphrase('')
      setConfirm('')
      onClose()
    } catch (e) {
      notifyError(t('backup.saveFailed'), e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <ResponsiveModal opened={opened} onClose={onClose} title={t('backup.title')}>
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          {t('backup.description')}
        </Text>
        <Alert variant="light" color="yellow" icon={<IconShieldLock size={18} />} p="xs">
          <Text size="xs">{t('backup.warning')}</Text>
        </Alert>
        <PasswordInput
          label={t('backup.passphrase')}
          value={passphrase}
          onChange={(e) => setPassphrase(e.currentTarget.value)}
          error={passphrase.length > 0 && tooShort ? t('backup.tooShort', { min: MIN_PASSPHRASE }) : null}
        />
        <PasswordInput
          label={t('backup.confirm')}
          value={confirm}
          onChange={(e) => setConfirm(e.currentTarget.value)}
          error={mismatch ? t('backup.mismatch') : null}
        />
        <Button onClick={save} loading={busy} disabled={!canSave}>
          {t('backup.save')}
        </Button>
      </Stack>
    </ResponsiveModal>
  )
}

/**
 * Shown automatically when this device has no local keys but the server holds a
 * backup — i.e. a fresh device, or after IndexedDB was evicted. Recovering here
 * avoids silently minting a new identity and losing the existing chats. On success
 * the page reloads so every part of the app picks up the restored state.
 */
export function KeyRestoreGate() {
  const { t } = useTranslation()
  const [needed, setNeeded] = useState(false)
  const [passphrase, setPassphrase] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let active = true
    const run = async () => {
      if (await hasLocalKeys()) return
      const exists = await backupExists()
      if (exists && active) setNeeded(true)
    }
    void run()
    return () => {
      active = false
    }
  }, [])

  async function restore() {
    if (busy || passphrase.length === 0) return
    setBusy(true)
    try {
      const ok = await restoreKeys(passphrase)
      if (!ok) {
        setNeeded(false) // backup vanished; nothing to restore
        return
      }
      window.location.reload()
    } catch {
      // Wrong passphrase (the GCM tag failed) — let them try again.
      notifyError(t('backup.wrongPassphrase'), null)
    } finally {
      setBusy(false)
    }
  }

  // "Skip" starts fresh on this device: a new identity, losing the backed-up
  // history here until they restore. Deliberate, so it is a distinct action.
  return (
    <ResponsiveModal opened={needed} onClose={() => setNeeded(false)} title={t('backup.restoreTitle')}>
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          {t('backup.restoreDescription')}
        </Text>
        <PasswordInput
          data-autofocus
          label={t('backup.passphrase')}
          value={passphrase}
          onChange={(e) => setPassphrase(e.currentTarget.value)}
          onKeyDown={(e) => e.key === 'Enter' && restore()}
        />
        <Button onClick={restore} loading={busy} disabled={passphrase.length === 0}>
          {t('backup.restore')}
        </Button>
        <Button variant="subtle" color="gray" onClick={() => setNeeded(false)}>
          {t('backup.skip')}
        </Button>
      </Stack>
    </ResponsiveModal>
  )
}
