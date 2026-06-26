import { Progress, Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { checkPassword } from '../lib/password'

const COLORS = ['red', 'red', 'yellow', 'lime', 'teal']

interface PasswordStrengthProps {
  value: string
}

/** A compact strength meter rendered beneath a password field. */
export function PasswordStrength({ value }: PasswordStrengthProps) {
  const { t } = useTranslation()
  if (!value) return null
  const { score } = checkPassword(value)
  const labels = [
    t('auth.strengthWeak'),
    t('auth.strengthWeak'),
    t('auth.strengthFair'),
    t('auth.strengthGood'),
    t('auth.strengthStrong'),
  ]
  return (
    <div>
      <Progress value={((score + 1) / 5) * 100} color={COLORS[score]} size="xs" mt={4} />
      <Text size="xs" c="dimmed" mt={4}>
        {t('auth.passwordStrength')}: {labels[score]}
      </Text>
    </div>
  )
}
