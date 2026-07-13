// Channels have no avatar image, so the chat list derives one: a stable colour
// plus the name's initials, the way Telegram does for chats without a photo.

// Mantine palette keys. Deliberately excludes red/yellow, which read as status.
export const AVATAR_COLORS = [
  'iris',
  'grape',
  'teal',
  'cyan',
  'blue',
  'orange',
  'pink',
  'lime',
] as const

export type AvatarColor = (typeof AVATAR_COLORS)[number]

/** FNV-1a, 32-bit. Cheap, well-distributed, and stable across sessions. */
function hash(value: string): number {
  let h = 0x811c9dc5
  for (let i = 0; i < value.length; i++) {
    h ^= value.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return h >>> 0
}

/**
 * Picks a channel's avatar colour. Keyed on the immutable id rather than the
 * name, so renaming a channel does not recolour it.
 */
export function channelColor(id: string): AvatarColor {
  return AVATAR_COLORS[hash(id) % AVATAR_COLORS.length]
}

/** Up to two uppercase initials from a channel name; '#' when it has no letters. */
export function channelInitials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean)
  if (words.length === 0) return '#'
  const letters = words.slice(0, 2).map((w) => [...w][0] ?? '')
  return letters.join('').toUpperCase() || '#'
}
