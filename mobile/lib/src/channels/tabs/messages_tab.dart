import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/providers.dart';
import '../../core/snackbar.dart';
import '../../data/app_providers.dart';
import '../../l10n/app_localizations.dart';
import '../../models/models.dart';
import '../../widgets/adaptive/adaptive.dart';
import '../../widgets/error_view.dart';
import '../../chat/chat_time.dart';
import '../../chat/widgets/call_event_bubble.dart';
import '../../widgets/message_carousel.dart';

/// Message history for a channel: searchable, paginated, and updated live from
/// the SSE stream (new messages prepend unless a search filter is active).
class MessagesTab extends ConsumerStatefulWidget {
  const MessagesTab({super.key, required this.channelId});

  final String channelId;

  @override
  ConsumerState<MessagesTab> createState() => _MessagesTabState();
}

class _MessagesTabState extends ConsumerState<MessagesTab> {
  final _search = TextEditingController();
  List<Message> _messages = const [];
  String _cursor = '';
  String _activeQuery = '';
  bool _loading = true;
  bool _loadingMore = false;
  bool _error = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  Future<void> _load({String query = ''}) async {
    setState(() {
      _loading = true;
      _error = false;
    });
    try {
      final page = await ref
          .read(repositoryProvider)
          .listMessages(widget.channelId, query: query);
      if (!mounted) return;
      setState(() {
        _messages = page.messages;
        _cursor = page.nextCursor;
        _activeQuery = query;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = true;
      });
    }
  }

  Future<void> _runSearch() async {
    final query = _search.text.trim();
    try {
      await _load(query: query);
    } catch (e) {
      if (mounted) {
        notifyError(context, context.l10n.t('channel.searchFailed'), e);
      }
    }
  }

  Future<void> _clearSearch() async {
    _search.clear();
    await _load();
  }

  Future<void> _loadMore() async {
    if (_cursor.isEmpty || _loadingMore) return;
    setState(() => _loadingMore = true);
    try {
      final page = await ref
          .read(repositoryProvider)
          .listMessages(widget.channelId, cursor: _cursor, query: _activeQuery);
      if (!mounted) return;
      setState(() {
        _messages = [..._messages, ...page.messages];
        _cursor = page.nextCursor;
        _loadingMore = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _loadingMore = false);
      notifyError(context, context.l10n.t('channels.loadFailed'), e);
    }
  }

  void _onLiveEvent(LiveEvent event) {
    // The stream multiplexes channel broadcasts, chat messages and call nudges onto one event, so a
    // channel tab has to check that this event is a channel message at all before reading it.
    final message = event.message;
    if (message == null) return;
    if (event.channelId != widget.channelId) return;
    if (_activeQuery.isNotEmpty) return;
    if (_messages.any((m) => m.id == message.id)) return;
    setState(() => _messages = [message, ..._messages]);
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;

    // Prepend live messages arriving over SSE.
    ref.listen(liveEventsProvider, (_, next) {
      next.whenData(_onLiveEvent);
    });

    if (_loading) {
      return const Center(child: AdaptiveProgress());
    }
    if (_error) {
      return ErrorView(
        message: l10n.t('channels.loadFailed'),
        onRetry: () => _load(query: _activeQuery),
      );
    }

    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
          child: isCupertino(context)
              ? CupertinoSearchTextField(
                  controller: _search,
                  placeholder: l10n.t('channel.searchHint'),
                  onSubmitted: (_) => _runSearch(),
                  onSuffixTap: _clearSearch,
                )
              : TextField(
                  controller: _search,
                  textInputAction: TextInputAction.search,
                  onSubmitted: (_) => _runSearch(),
                  decoration: InputDecoration(
                    hintText: l10n.t('channel.searchHint'),
                    prefixIcon: const Icon(Icons.search, size: 20),
                    suffixIcon:
                        (_search.text.isNotEmpty || _activeQuery.isNotEmpty)
                        ? IconButton(
                            icon: const Icon(Icons.close, size: 18),
                            onPressed: _clearSearch,
                          )
                        : null,
                    isDense: true,
                  ),
                ),
        ),
        if (_activeQuery.isNotEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Align(
              alignment: Alignment.centerLeft,
              child: Text(
                l10n.tp('channel.filtering', {'query': _activeQuery}),
                style: TextStyle(
                  fontSize: 12,
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
              ),
            ),
          ),
        Expanded(child: _list(context, l10n)),
      ],
    );
  }

  Widget _list(BuildContext context, AppLocalizations l10n) {
    if (_messages.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            _activeQuery.isNotEmpty
                ? l10n.t('channel.noMessagesSearch')
                : l10n.t('channel.noMessages'),
            style: TextStyle(
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          ),
        ),
      );
    }
    return AdaptiveRefreshableScrollView(
      onRefresh: () => _load(query: _activeQuery),
      slivers: [
        SliverPadding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
          sliver: SliverList(
            delegate: SliverChildBuilderDelegate((context, i) {
              // Preserve the 8px inter-item spacing the old ListView.separated
              // provided, without a real separator builder for slivers.
              final top = i == 0 ? 0.0 : 8.0;
              if (i >= _messages.length) {
                return Padding(
                  padding: EdgeInsets.only(top: top),
                  child: Padding(
                    padding: const EdgeInsets.symmetric(vertical: 8),
                    child: Center(
                      child: _loadingMore
                          ? const AdaptiveProgress(size: 24)
                          : AdaptiveButton.outlined(
                              onPressed: _loadMore,
                              child: Text(l10n.t('channel.loadMore')),
                            ),
                    ),
                  ),
                );
              }
              // A day separator wherever the date changes, exactly as the chat feed marks one.
              // The list runs newest-first, so the message BELOW this one in time is the next
              // index — the separator therefore belongs above index i when i is the last post of
              // its day.
              final message = _messages[i];
              final day = messageDay(message.createdAt);
              final newerDay = i == 0
                  ? null
                  : messageDay(_messages[i - 1].createdAt);
              final endsDay =
                  day != null && (newerDay == null || newerDay != day);
              return Padding(
                padding: EdgeInsets.only(top: top),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    _MessageCard(message: message),
                    if (endsDay) DateSeparator(day: day),
                  ],
                ),
              );
            }, childCount: _messages.length + (_cursor.isNotEmpty ? 1 : 0)),
          ),
        ),
      ],
    );
  }
}

/// One post, drawn as the message it is.
///
/// It was a full-width Card: a cover image across the top, then the title, then two lines of body.
/// That is how a blog lists articles, and it is why a channel read like a feed reader while the
/// chat beside it read like a conversation.
///
/// Now it is a bubble, aligned left and only as wide as it needs to be — the same shape as an
/// incoming chat message, for the same reason: somebody sent this to you. It stays left-aligned
/// even for the channel's owner, because a broadcast has one voice and there is no other side to
/// align against.
///
/// The title keeps its weight and the comment count sits beneath it, as on the web. What is gone is
/// the two-line body clamp: a bubble that shows the whole of a short post and opens for a long one
/// beats a card that truncates everything to exactly two lines.
class _MessageCard extends StatelessWidget {
  const _MessageCard({required this.message});

  final Message message;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final title = message.title.isEmpty
        ? l10n.t('channel.noTitle')
        : message.title;

    return Align(
      alignment: Alignment.centerLeft,
      child: ConstrainedBox(
        // The same ceiling a chat bubble keeps, so a one-word post does not stretch the screen and
        // a long one still wraps well short of the edge.
        constraints: BoxConstraints(
          maxWidth: MediaQuery.sizeOf(context).width * 0.82,
        ),
        child: Material(
          color: scheme.surface,
          borderRadius: const BorderRadius.only(
            topLeft: Radius.circular(4),
            topRight: Radius.circular(16),
            bottomLeft: Radius.circular(16),
            bottomRight: Radius.circular(16),
          ),
          clipBehavior: Clip.antiAlias,
          child: InkWell(
            onTap: () => context.push(
              '/channels/${message.channelId}/messages/${message.id}',
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                if (message.images.isNotEmpty)
                  MessageCover(images: message.images),
                Padding(
                  padding: const EdgeInsets.fromLTRB(12, 8, 12, 6),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(
                        title,
                        style: const TextStyle(
                          fontWeight: FontWeight.w600,
                          fontSize: 15,
                        ),
                      ),
                      if (message.body.isNotEmpty) ...[
                        const SizedBox(height: 4),
                        Text(
                          message.body,
                          style: const TextStyle(fontSize: 14),
                        ),
                      ],
                      const SizedBox(height: 4),
                      Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          if (message.commentsAllowed) ...[
                            Icon(
                              Icons.mode_comment_outlined,
                              size: 13,
                              color: scheme.primary,
                            ),
                            const SizedBox(width: 4),
                          ],
                          const Spacer(),
                          Text(
                            bubbleTime(l10n, message.createdAt),
                            style: TextStyle(
                              fontSize: 11,
                              color: scheme.onSurfaceVariant,
                            ),
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
