import { Avatar, UnstyledButton } from '@mantine/core'
import { channelColor, channelInitials } from '../../lib/avatar'
import { imageUrl } from '../../lib/api'

interface ChannelAvatarProps {
  /** Drives the fallback colour: stable across renames. */
  id: string
  name: string
  /** Uploaded picture. Without one, the generated colour + initials stand in. */
  avatarId?: string
  size?: number
  /**
   * Tapping the avatar opens the info screen for whatever it stands for — a
   * channel's details, a contact's profile, a group's roster. Given here, the
   * avatar becomes a real button (focusable, labelled); left off, it is inert
   * decoration as before, so list rows keep the row itself as the target.
   */
  onClick?: () => void
  /** Accessible label for the button form (e.g. "View info: Alice"). */
  label?: string
}

export function ChannelAvatar({ id, name, avatarId, size = 48, onClick, label }: ChannelAvatarProps) {
  // radius 9999 (clamped to a true 50%), not "xl": the xl token is a fixed 32px,
  // which is a circle on a 48px avatar but visibly a squircle on the 72px one in
  // the channel-info panel.
  const avatar = (
    <Avatar
      src={avatarId ? imageUrl(avatarId) : undefined}
      alt={name}
      color={channelColor(id)}
      radius={9999}
      size={size}
      variant="filled"
    >
      {channelInitials(name)}
    </Avatar>
  )

  if (!onClick) return avatar

  return (
    <UnstyledButton
      onClick={onClick}
      aria-label={label ?? name}
      className="pheme-avatar-button"
      style={{ borderRadius: 9999, flex: '0 0 auto', display: 'inline-flex' }}
    >
      {avatar}
    </UnstyledButton>
  )
}
