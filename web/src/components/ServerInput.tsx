import { TextInput } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { isValidServerUrl } from '../lib/server'

interface Props {
  value: string
  onChange: (value: string) => void
  disabled?: boolean
  onEnter?: () => void
}

/**
 * The server address, as a form field.
 *
 * On every page that authenticates — sign in, register, reset a password — because which server you
 * are talking to is part of who you are. The same email address on two Pheme instances is two
 * different people, and a password reset sent to the wrong one is a reset of nothing.
 *
 * Only complains once there is something to complain about. A field that turns red the moment it is
 * touched is scolding somebody for not having finished typing.
 */
export function ServerInput({ value, onChange, disabled, onEnter }: Props) {
  const { t } = useTranslation()
  const invalid = value.trim() !== '' && !isValidServerUrl(value)

  return (
    <TextInput
      label={t('auth.server')}
      placeholder={t('auth.serverPlaceholder')}
      description={t('auth.serverHint')}
      value={value}
      disabled={disabled}
      error={invalid ? t('auth.serverInvalid') : undefined}
      onChange={(e) => onChange(e.currentTarget.value)}
      onKeyDown={(e) => e.key === 'Enter' && onEnter?.()}
      inputMode="url"
      autoComplete="url"
      spellCheck={false}
    />
  )
}
