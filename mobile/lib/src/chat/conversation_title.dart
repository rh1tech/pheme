// What a conversation is called.

import '../l10n/app_localizations.dart';
import '../models/chat_models.dart';
import '../models/models.dart';

/// A person's display label: their name, else their @username, else a stub from their id.
///
/// Never their email. The server does not return it from a user search, and it should not: an
/// address is not a handle.
String userLabel(PublicUser user) {
  final display = user.displayName;
  if (display != null && display.isNotEmpty) return display;

  final username = user.username;
  if (username != null && username.isNotEmpty) return '@$username';

  final id = user.id;
  return 'User ${id.length > 6 ? id.substring(0, 6) : id}';
}

/// A stub for somebody no longer on the roster — a removed member, whose name the client has no
/// way to look up any more. Matches the shape userLabel falls back to.
String shortUserLabel(String userId) =>
    'User ${userId.length > 6 ? userId.substring(0, 6) : userId}';

/// A direct chat is named after the other person; a group after its title, falling back to the
/// members' names so an untitled group is still recognisable.
String conversationTitle(
  Conversation conversation,
  String myUserId,
  AppLocalizations l10n,
) {
  if (!conversation.isGroup) {
    final other = conversation.otherMember(myUserId);
    return other == null ? l10n.t('chat.newChat') : userLabel(other.user);
  }

  final title = conversation.title;
  if (title != null && title.isNotEmpty) return title;

  final others = conversation.members
      .where((m) => m.userId != myUserId)
      .map((m) => userLabel(m.user));
  return others.isEmpty ? l10n.t('chat.newGroup') : others.join(', ');
}
