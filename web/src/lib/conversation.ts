import type { Conversation, Member, PublicUser } from './types'

/** The best display name for a public user: display name, then @username, then a stub. */
export function userLabel(user: PublicUser | undefined): string {
  if (!user) return 'Unknown'
  if (user.displayName) return user.displayName
  if (user.username) return `@${user.username}`
  return 'User ' + user.id.slice(0, 6)
}

/**
 * What to call a channel subscriber on screen.
 *
 * The same rule as [userLabel], against a Member's differently-named fields — a Member keys the
 * person as `userId` rather than `id`. Never empty: a row with nothing to print cannot be acted on.
 */
export function memberLabel(member: Member): string {
  return userLabel({
    id: member.userId,
    username: member.username,
    displayName: member.displayName,
  })
}

/** The other member of a direct chat, from the caller's point of view. */
export function otherMember(conv: Conversation, meId: string): PublicUser | undefined {
  const other = conv.members.find((m) => m.userId !== meId)
  return other?.user
}

/**
 * What a conversation is called: a group's title, or the other person's name for
 * a direct chat. Groups without a title fall back to a member-name list.
 */
export function conversationTitle(conv: Conversation, meId: string): string {
  if (conv.kind === 'direct') return userLabel(otherMember(conv, meId))
  if (conv.title) return conv.title
  const names = conv.members
    .filter((m) => m.userId !== meId)
    .map((m) => userLabel(m.user))
  return names.length ? names.join(', ') : 'Group'
}

/** The id an avatar is keyed on: the other user (direct) or the conversation (group). */
export function conversationAvatarKey(conv: Conversation, meId: string): string {
  if (conv.kind === 'direct') return otherMember(conv, meId)?.id ?? conv.id
  return conv.id
}
