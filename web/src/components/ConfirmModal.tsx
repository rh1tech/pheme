import { Button, Group, Modal, Stack } from '@mantine/core'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

interface ConfirmModalProps {
  opened: boolean
  onClose: () => void
  onConfirm: () => void
  title: string
  /** Body content — typically the confirmation question. */
  children: ReactNode
  confirmLabel?: string
  confirmColor?: string
  loading?: boolean
}

/**
 * A reusable confirmation dialog: a titled modal with a cancel button and a
 * (by default destructive) confirm button. Used for all delete confirmations.
 */
export function ConfirmModal({
  opened,
  onClose,
  onConfirm,
  title,
  children,
  confirmLabel,
  confirmColor = 'red',
  loading = false,
}: ConfirmModalProps) {
  const { t } = useTranslation()
  return (
    <Modal opened={opened} onClose={onClose} title={title}>
      <Stack>
        {children}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button color={confirmColor} loading={loading} onClick={onConfirm}>
            {confirmLabel ?? t('common.delete')}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
