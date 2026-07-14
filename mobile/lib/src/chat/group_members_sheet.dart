// The group roster: who is in it, who runs it, and adding or removing people.
//
// Every action here drives an MLS Commit as well as a membership change, and the two have to happen
// in the right order — see MlsService.addGroupMember / removeGroupMember. Nothing in this file does
// the ordering itself; it all goes through the service, which is the only place that knows.

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';
import '../core/snackbar.dart';
import '../crypto/mls_errors.dart';
import '../l10n/app_localizations.dart';
import '../models/chat_models.dart';
import '../models/models.dart';
import '../widgets/adaptive/adaptive_controls.dart';
import '../widgets/adaptive/adaptive_text_field.dart';
import 'chat_providers.dart';
import 'conversation_title.dart';
import 'widgets/conversation_avatar.dart';

const _minQuery = 2;
const _debounce = Duration(milliseconds: 250);

Future<void> showGroupMembersSheet(
  BuildContext context,
  Conversation conversation,
) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    showDragHandle: true,
    builder: (_) => _GroupMembersSheet(conversation: conversation),
  );
}

class _GroupMembersSheet extends ConsumerStatefulWidget {
  const _GroupMembersSheet({required this.conversation});

  final Conversation conversation;

  @override
  ConsumerState<_GroupMembersSheet> createState() => _GroupMembersSheetState();
}

class _GroupMembersSheetState extends ConsumerState<_GroupMembersSheet> {
  final _query = TextEditingController();
  Timer? _debounceTimer;

  List<PublicUser> _results = const [];
  bool _searching = false;
  bool _busy = false;

  late Conversation _conversation = widget.conversation;

  @override
  void dispose() {
    _debounceTimer?.cancel();
    _query.dispose();
    super.dispose();
  }

  Future<void> _refresh() async {
    final updated = await ref
        .read(repositoryProvider)
        .getConversation(_conversation.id);
    if (!mounted) return;
    setState(() => _conversation = updated);
    ref.invalidate(conversationProvider(_conversation.id));
    await ref.read(conversationListProvider.notifier).refresh();
  }

  void _onQueryChanged(String value) {
    _debounceTimer?.cancel();
    final query = value.trim();

    if (query.length < _minQuery) {
      setState(() {
        _results = const [];
        _searching = false;
      });
      return;
    }
    setState(() => _searching = true);
    _debounceTimer = Timer(_debounce, () => _search(query));
  }

  Future<void> _search(String query) async {
    try {
      final users = await ref.read(repositoryProvider).searchUsers(query);
      if (!mounted) return;

      final members = _conversation.members.map((m) => m.userId).toSet();
      setState(() {
        _results = users.where((u) => !members.contains(u.id)).toList();
        _searching = false;
      });
    } on Object {
      if (!mounted) return;
      setState(() => _searching = false);
    }
  }

  Future<void> _run(Future<void> Function() action, String successKey) async {
    setState(() => _busy = true);
    final l10n = AppLocalizations.of(context);
    try {
      await action();
      await _refresh();
      if (!mounted) return;
      notifySuccess(context, l10n.t(successKey));
      setState(() {
        _query.clear();
        _results = const [];
      });
    } on Object catch (e) {
      if (!mounted) return;
      // A person with no keys cannot be added, and that is about them, not about the action failing.
      final message = e is PeerKeysMissingException
          ? l10n.t('chat.peerNotReady')
          : l10n.t('group.actionFailed');
      notifyError(context, message, e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final myUserId = ref.watch(myUserIdProvider);
    final mls = ref.watch(mlsServiceProvider);

    final iAmAdmin = _conversation.isAdmin(myUserId);
    final owner = _conversation.createdBy;
    final insets = MediaQuery.viewInsetsOf(context).bottom;

    return Padding(
      padding: EdgeInsets.only(bottom: insets),
      child: SafeArea(
        top: false,
        child: ConstrainedBox(
          constraints: BoxConstraints(
            maxHeight: MediaQuery.sizeOf(context).height * 0.85,
          ),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
                child: Text(
                  l10n.t('group.membersTitle'),
                  style: theme.textTheme.titleMedium?.copyWith(
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),

              // Only an admin may add anyone — the server enforces it, and offering the control to
              // someone who will be refused is just a lie with extra steps.
              if (iAmAdmin) ...[
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
                  child: AdaptiveTextField(
                    controller: _query,
                    placeholder: l10n.t('chat.searchPeople'),
                    onChanged: _onQueryChanged,
                  ),
                ),
                if (_searching)
                  const Padding(
                    padding: EdgeInsets.all(12),
                    child: Center(child: AdaptiveProgress(size: 18)),
                  ),
                for (final user in _results)
                  ListTile(
                    leading: ConversationAvatar(
                      id: user.id,
                      label: userLabel(user),
                      size: 34,
                    ),
                    title: Text(userLabel(user)),
                    trailing: const Icon(Icons.person_add_alt),
                    enabled: !_busy,
                    onTap: () => _run(
                      () => mls.addGroupMember(
                        _conversation.id,
                        myUserId,
                        user.id,
                      ),
                      'group.added',
                    ),
                  ),
                const Divider(height: 16),
              ],

              Padding(
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 4),
                child: Text(
                  l10n.tp('group.members', {
                    'count': '${_conversation.members.length}',
                  }),
                  style: theme.textTheme.labelMedium?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
              ),

              Flexible(
                child: ListView(
                  shrinkWrap: true,
                  children: [
                    for (final member in _conversation.members)
                      _MemberRow(
                        member: member,
                        isMe: member.userId == myUserId,
                        // The owner is nobody's to demote or remove, and neither are you — leaving is
                        // its own action, not a removal.
                        canAct:
                            iAmAdmin &&
                            member.userId != myUserId &&
                            member.userId != owner,
                        busy: _busy,
                        onToggleAdmin: () => _run(
                          () => ref
                              .read(repositoryProvider)
                              .setConversationMemberRole(
                                _conversation.id,
                                member.userId,
                                member.isAdmin
                                    ? ChannelRole.user
                                    : ChannelRole.admin,
                              ),
                          'group.roleChanged',
                        ),
                        onRemove: () => _run(
                          () => mls.removeGroupMember(
                            _conversation.id,
                            myUserId,
                            member.userId,
                          ),
                          'group.removed',
                        ),
                      ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _MemberRow extends StatelessWidget {
  const _MemberRow({
    required this.member,
    required this.isMe,
    required this.canAct,
    required this.busy,
    required this.onToggleAdmin,
    required this.onRemove,
  });

  final ConversationMember member;
  final bool isMe;
  final bool canAct;
  final bool busy;
  final VoidCallback onToggleAdmin;
  final VoidCallback onRemove;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    final label = userLabel(member.user);
    final name = isMe ? '$label (${l10n.t('group.you')})' : label;

    return ListTile(
      leading: ConversationAvatar(id: member.userId, label: label, size: 34),
      title: Row(
        children: [
          Flexible(child: Text(name, overflow: TextOverflow.ellipsis)),
          if (member.isAdmin) ...[
            const SizedBox(width: 6),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
              decoration: BoxDecoration(
                color: theme.colorScheme.primaryContainer,
                borderRadius: BorderRadius.circular(999),
              ),
              child: Text(
                l10n.t('group.admin'),
                style: theme.textTheme.labelSmall?.copyWith(
                  color: theme.colorScheme.onPrimaryContainer,
                ),
              ),
            ),
          ],
        ],
      ),
      trailing: !canAct
          ? null
          : PopupMenuButton<String>(
              enabled: !busy,
              tooltip: l10n.t('group.memberActions'),
              onSelected: (value) =>
                  value == 'role' ? onToggleAdmin() : onRemove(),
              itemBuilder: (_) => [
                PopupMenuItem(
                  value: 'role',
                  child: Text(
                    l10n.t(
                      member.isAdmin ? 'group.removeAdmin' : 'group.makeAdmin',
                    ),
                  ),
                ),
                PopupMenuItem(
                  value: 'remove',
                  child: Text(
                    l10n.t('group.remove'),
                    style: TextStyle(color: theme.colorScheme.error),
                  ),
                ),
              ],
            ),
    );
  }
}
