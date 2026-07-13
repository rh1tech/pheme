import { Avatar } from '@mantine/core'
import { channelColor, channelInitials } from '../../lib/avatar'
import { imageUrl } from '../../lib/api'

interface ChannelAvatarProps {
  /** Drives the fallback colour: stable across renames. */
  id: string
  name: string
  /** Uploaded picture. Without one, the generated colour + initials stand in. */
  avatarId?: string
  size?: number
}

export function ChannelAvatar({ id, name, avatarId, size = 48 }: ChannelAvatarProps) {
  return (
    // radius 9999 (clamped to a true 50%), not "xl": the xl token is a fixed 32px,
    // which is a circle on a 48px avatar but visibly a squircle on the 72px one in
    // the channel-info panel.
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
}
