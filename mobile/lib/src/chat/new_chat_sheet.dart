// Starting a chat: pick one person for a direct chat, or several plus a title for a group.

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/api_exception.dart';
import '../core/providers.dart';
import '../core/snackbar.dart';
import '../crypto/mls_errors.dart';
import '../l10n/app_localizations.dart';
import '../models/models.dart';
import '../widgets/adaptive/adaptive_controls.dart';
import '../widgets/adaptive/adaptive_text_field.dart';
import 'chat_providers.dart';
import 'conversation_title.dart';
import 'handles.dart';
import 'widgets/conversation_avatar.dart';

/// The server wants at least two characters, and a search on every keystroke is a search on every
/// keystroke. Both numbers match the web client.
const _minQuery = 2;
const _debounce = Duration(milliseconds: 250);

Future<void> showNewChatSheet(BuildContext context) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    // Without this the sheet runs under the status bar and the notch on a tall screen, which is
    // visible the moment it is given a fixed near-full height.
    useSafeArea: true,
    showDragHandle: true,
    builder: (_) => const _NewChatSheet(),
  );
}

class _NewChatSheet extends ConsumerStatefulWidget {
  const _NewChatSheet();

  @override
  ConsumerState<_NewChatSheet> createState() => _NewChatSheetState();
}

class _NewChatSheetState extends ConsumerState<_NewChatSheet> {
  final _query = TextEditingController();
  final _groupTitle = TextEditingController();

  Timer? _debounceTimer;
  List<PublicUser> _results = const [];
  final _picked = <PublicUser>[];

  bool _searching = false;
  // Whether the last COMPLETED search found nobody. Tracked separately from
  // [_searching] so "No one found" stays put while the next keystroke's search runs,
  // instead of blinking off and back. Only a search that returns people clears it.
  bool _emptyResult = false;
  bool _busy = false;
  bool _groupMode = false;

  @override
  void dispose() {
    _debounceTimer?.cancel();
    _query.dispose();
    _groupTitle.dispose();
    super.dispose();
  }

  void _onQueryChanged(String value) {
    _debounceTimer?.cancel();
    final query = value.trim();

    // Too short, or a handle: no server query. A `username@host` handle is resolved
    // on demand when the user taps "start a chat with…", never by local search — so an
    // '@' stops us hitting the server at all.
    if (query.length < _minQuery || query.contains('@')) {
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

      final myUserId = ref.read(myUserIdProvider);
      final pickedIds = _picked.map((u) => u.id).toSet();
      setState(() {
        _results = users
            .where((u) => u.id != myUserId && !pickedIds.contains(u.id))
            .toList();
        _emptyResult = _results.isEmpty;
        _searching = false;
      });
    } on Object {
      if (!mounted) return;
      setState(() {
        _emptyResult = false;
        _searching = false;
      });
    }
  }

  /// Starts a direct chat with someone on another host, addressed by their handle.
  /// The server resolves `username@host` and provisions the mirror — the same path a
  /// local direct chat takes, just with a handle instead of an id.
  Future<void> _startWithRemote(String handle) async {
    setState(() => _busy = true);
    try {
      final conversation = await ref
          .read(conversationListProvider.notifier)
          .startDirect(handle);
      if (!mounted) return;
      Navigator.of(context).pop();
      context.push('/chats/${conversation.id}');
    } on Object catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      _reportFailure(e);
    }
  }

  Future<void> _startDirect(PublicUser user) async {
    setState(() => _busy = true);
    try {
      final conversation = await ref
          .read(conversationListProvider.notifier)
          .startDirect(user.id);
      if (!mounted) return;
      Navigator.of(context).pop();
      context.push('/chats/${conversation.id}');
    } on Object catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      _reportFailure(e);
    }
  }

  Future<void> _createGroup() async {
    final title = _groupTitle.text.trim();
    if (title.isEmpty || _picked.isEmpty) return;

    setState(() => _busy = true);
    try {
      final conversation = await ref
          .read(conversationListProvider.notifier)
          .createGroup(title, _picked.map((u) => u.id).toList());
      if (!mounted) return;
      Navigator.of(context).pop();
      context.push('/chats/${conversation.id}');
    } on Object catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      _reportFailure(e);
    }
  }

  void _reportFailure(Object error) {
    final l10n = AppLocalizations.of(context);
    // A peer with no keys is not a failure of ours, and saying "could not start the chat" would send
    // the user looking for a problem on their end. Tell them what actually has to happen. A 404 on a
    // handle is the far host saying "no user by that username" — usually just an unset one.
    final String message;
    if (error is PeerKeysMissingException) {
      message = l10n.t('chat.peerNotReady');
    } else if (error is ApiException && error.statusCode == 404) {
      message = l10n.t('chat.remoteUserNotFound');
    } else {
      message = l10n.t('chat.startFailed');
    }
    notifyError(context, message, error);
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    // A FIXED height, and the keyboard is left to overlay it rather than to resize it.
    //
    // Before, the sheet was as tall as its contents (mainAxisSize.min over a Flexible results
    // area) and the keyboard's height was subtracted from its box. So it opened nearly full
    // screen, and the moment the user typed, it collapsed to around 60% — partly because the
    // keyboard appeared, partly because the results area shrinks to a placeholder while the query
    // is under two characters. The sheet jumped about while being typed into.
    //
    // Now it stays where it opened. The results list carries the keyboard inset as bottom padding,
    // so everything can still be scrolled out from behind the keyboard.
    return SafeArea(
      top: false,
      child: SizedBox(
        height: MediaQuery.sizeOf(context).height * 0.92,
        child: Column(
          mainAxisSize: MainAxisSize.max,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    l10n.t(
                      _groupMode ? 'chat.newGroupTitle' : 'chat.newChatTitle',
                    ),
                    style: theme.textTheme.titleMedium?.copyWith(
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 12),
                  SegmentedButton<bool>(
                    segments: [
                      ButtonSegment(
                        value: false,
                        label: Text(l10n.t('chat.newChat')),
                      ),
                      ButtonSegment(
                        value: true,
                        label: Text(l10n.t('chat.newGroup')),
                      ),
                    ],
                    selected: {_groupMode},
                    onSelectionChanged: (s) => setState(() {
                      _groupMode = s.first;
                      _picked.clear();
                    }),
                  ),
                  if (_groupMode) ...[
                    const SizedBox(height: 12),
                    // Labelled, not just hinted. The placeholder is an EXAMPLE — "Weekend plans" —
                    // so on its own it says what a group name might look like and never what the
                    // box is for, and it disappears the moment anything is typed. The string
                    // already existed and was simply never used here.
                    AdaptiveTextField(
                      controller: _groupTitle,
                      label: l10n.t('chat.groupName'),
                      placeholder: l10n.t('chat.groupNamePlaceholder'),
                      onChanged: (_) => setState(() {}),
                    ),
                    if (_picked.isNotEmpty) ...[
                      const SizedBox(height: 8),
                      Wrap(
                        spacing: 6,
                        runSpacing: 6,
                        children: [
                          for (final user in _picked)
                            Chip(
                              label: Text(userLabel(user)),
                              onDeleted: () =>
                                  setState(() => _picked.remove(user)),
                            ),
                        ],
                      ),
                    ],
                  ],
                  const SizedBox(height: 12),
                  AdaptiveTextField(
                    controller: _query,
                    label: l10n.t('chat.addPeople'),
                    placeholder: l10n.t('chat.searchPeople'),
                    onChanged: _onQueryChanged,
                  ),
                ],
              ),
            ),
            Expanded(child: _results_(l10n, theme)),
            if (_groupMode)
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 16),
                child: AdaptiveButton.filled(
                  onPressed:
                      _busy ||
                          _picked.isEmpty ||
                          _groupTitle.text.trim().isEmpty
                      ? null
                      : _createGroup,
                  child: _busy
                      ? const AdaptiveProgress(size: 18)
                      : Text(l10n.t('chat.createGroup')),
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget _results_(AppLocalizations l10n, ThemeData theme) {
    // The sheet no longer shrinks for the keyboard, so the list has to make room for it itself.
    final insets = MediaQuery.viewInsetsOf(context).bottom;
    final trimmed = _query.text.trim();

    // A `username@host` handle: offer to start a chat with them directly, no search.
    // Direct chats only — a remote member joins a GROUP through its "Add member", not
    // at create time, because createConversation cannot provision a mirror.
    final handle = _groupMode ? null : remoteHandle(trimmed);
    if (handle != null) {
      return ListView(
        padding: EdgeInsets.only(bottom: insets),
        children: [
          ListTile(
            leading: const CircleAvatar(
              radius: 18,
              child: Icon(Icons.public, size: 20),
            ),
            title: Text(l10n.tp('chat.startRemote', {'handle': handle})),
            enabled: !_busy,
            onTap: () => _startWithRemote(handle),
          ),
        ],
      );
    }

    // Spinner only until the FIRST result/empty verdict; a re-search then keeps the
    // current list or "No one found" showing instead of blanking to a spinner.
    if (_searching && _results.isEmpty && !_emptyResult) {
      return const Padding(
        padding: EdgeInsets.all(24),
        child: Center(child: AdaptiveProgress()),
      );
    }

    if (_results.isEmpty) {
      // Held steady on the last completed empty search, so it does not blink while the
      // next keystroke's search runs.
      final show =
          _emptyResult && trimmed.length >= _minQuery && !trimmed.contains('@');
      return Padding(
        padding: const EdgeInsets.all(24),
        child: Center(
          child: Text(
            show ? l10n.t('chat.noPeople') : '',
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ),
      );
    }

    return ListView.builder(
      padding: EdgeInsets.only(bottom: insets),
      itemCount: _results.length,
      itemBuilder: (context, i) {
        final user = _results[i];
        final label = userLabel(user);

        return ListTile(
          leading: ConversationAvatar(
            id: user.id,
            label: label,
            size: 36,
            imageUrl: (user.avatarId?.isNotEmpty ?? false)
                ? ref.read(repositoryProvider).imageUrl(user.avatarId!)
                : null,
          ),
          title: Text(label),
          trailing: _groupMode ? const Icon(Icons.add) : null,
          enabled: !_busy,
          onTap: () {
            if (_groupMode) {
              setState(() {
                _picked.add(user);
                _results = _results.where((u) => u.id != user.id).toList();
              });
            } else {
              _startDirect(user);
            }
          },
        );
      },
    );
  }
}
