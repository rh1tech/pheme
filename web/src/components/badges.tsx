import { Badge } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import type { ChannelStatus, Role, SubscriptionMode, UserStatus } from '../lib/types'

/** Subscription mode (open / approval). Used on user and admin channel views. */
export function ModeBadge({ mode }: { mode: SubscriptionMode }) {
  const { t } = useTranslation()
  return <Badge color={mode === 'open' ? 'teal' : 'grape'}>{t(`mode.${mode}`)}</Badge>
}

/** Channel status (active / disabled). */
export function ChannelStatusBadge({ status }: { status: ChannelStatus }) {
  const { t } = useTranslation()
  return (
    <Badge color={status === 'disabled' ? 'red' : 'teal'} variant="light">
      {status === 'disabled' ? t('admin.statusDisabled') : t('admin.statusActive')}
    </Badge>
  )
}

/** User account status (active / blocked). */
export function UserStatusBadge({ status }: { status: UserStatus }) {
  const { t } = useTranslation()
  return (
    <Badge color={status === 'blocked' ? 'red' : 'teal'} variant="light">
      {status === 'blocked' ? t('admin.statusBlocked') : t('admin.statusActive')}
    </Badge>
  )
}

/** User role (admin / user). */
export function RoleBadge({ role }: { role: Role }) {
  const { t } = useTranslation()
  return (
    <Badge color={role === 'admin' ? 'grape' : 'gray'}>
      {role === 'admin' ? t('admin.roleAdmin') : t('admin.roleUser')}
    </Badge>
  )
}
