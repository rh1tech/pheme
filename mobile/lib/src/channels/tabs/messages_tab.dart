import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/providers.dart';
import '../../core/snackbar.dart';
import '../../data/app_providers.dart';
import '../../l10n/app_localizations.dart';
import '../../models/models.dart';
import '../../widgets/adaptive/adaptive.dart';
import '../../widgets/adaptive/adaptive_search_field.dart';
import '../../widgets/error_view.dart';
import '../../chat/chat_time.dart';
import '../../chat/widgets/chat_wallpaper.dart';
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

  /// Waits for typing to settle before asking the server.
  ///
  /// The two lists filter as you type because they filter a list they already hold. This search is
  /// a request per query, so typing "weather" unthrottled is seven searches of which six are
  /// thrown away — and on a slow link the answer to "weath" can land after the answer to
  /// "weather" and overwrite it.
  Timer? _debounce;

  /// A reversed list starts at offset 0 showing the NEWEST item, so opening on the latest post
  /// needs no scrolling at all — the controller is here for the load-more button's sake and for
  /// anything later that wants to jump.
  final _scroll = ScrollController();

  /// Guards the automatic load, for the same reason the comments do: a fast flick to the top would
  /// otherwise fire several requests for the same cursor and the feed would gain each page twice.
  void _onScroll() {
    if (!_scroll.hasClients || _loadingMore || _cursor.isEmpty) return;
    final pos = _scroll.position;
    if (pos.pixels >= pos.maxScrollExtent - 300) _loadMore();
  }

  List<Message> _messages = const [];
  String _cursor = '';
  String _activeQuery = '';
  bool _loading = true;
  bool _loadingMore = false;
  bool _error = false;

  @override
  void initState() {
    super.initState();
    _scroll.addListener(_onScroll);
    _load();
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _search.dispose();
    _scroll.dispose();
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
        // The field the Chats and Channels lists use. This screen had grown its own — a bare
        // TextField on Android and a CupertinoSearchTextField on iOS, each with its own clear
        // button — so searching inside a channel looked like a different app from searching for
        // one.
        Padding(
          padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
          child: AdaptiveSearchField(
            controller: _search,
            placeholder: l10n.t('channel.searchHint'),
            onChanged: _onSearchChanged,
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

  void _onSearchChanged(String value) {
    setState(() {});
    _debounce?.cancel();
    final trimmed = value.trim();
    if (trimmed.isEmpty) {
      // Clearing is immediate: there is nothing to ask the server, and waiting to restore the full
      // feed would feel like a stall.
      if (_activeQuery.isNotEmpty) _clearSearch();
      return;
    }
    _debounce = Timer(const Duration(milliseconds: 400), _runSearch);
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
    return ChatWallpaper(
      child: ListView.builder(
        // Reversed, like the chat feed: the newest post sits at the BOTTOM and the channel opens on
        // it. It used to render newest-first top-down, so opening a channel showed the latest post at
        // the top of the screen and reading forward meant scrolling up — the opposite of every other
        // message list in the app.
        reverse: true,
        controller: _scroll,
        padding: const EdgeInsets.fromLTRB(12, 8, 12, 12),
        itemCount: _messages.length + (_cursor.isNotEmpty ? 1 : 0),
        itemBuilder: (context, i) {
          // In a reversed list the last index draws at the TOP, which is where "load older" belongs.
          if (i >= _messages.length) {
            return Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              // Older posts arrive on their own now; this says they are coming rather than asking.
              child: const Center(child: AdaptiveProgress(size: 22)),
            );
          }

          final message = _messages[i];
          // The array runs newest-first and the list is reversed, so index+1 is the post ABOVE this
          // one on screen — the older one. A separator belongs above the first post of a day, which
          // means comparing against that older neighbour. Comparing against the newer one put
          // "Today" underneath the only message of today.
          final older = i + 1 < _messages.length ? _messages[i + 1] : null;
          final day = messageDay(message.createdAt);
          final olderDay = older == null ? null : messageDay(older.createdAt);
          final startsDay =
              day != null && (olderDay == null || day != olderDay);

          return Column(
            children: [
              if (startsDay) DateSeparator(day: day),
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: _MessageCard(message: message),
              ),
            ],
          );
        },
      ),
    );
  }
}

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

    // The same colour an INCOMING chat bubble uses, taken from MessageBubble rather than guessed:
    // surface is the page itself in some themes, which drew a bubble that could not be seen
    // against it. A channel post is incoming by definition — nobody reading it wrote it.
    // The web's bubble colour and shadow. A flat rectangle on a patterned background reads as a
    // hole punched in the wallpaper; the shadow is what puts it on top.
    final bubble = BubbleStyle.background(context);

    return Align(
      alignment: Alignment.centerLeft,
      child: ConstrainedBox(
        // The same ceiling a chat bubble keeps, so a one-word post does not stretch the screen and
        // a long one still wraps well short of the edge.
        constraints: BoxConstraints(
          maxWidth: MediaQuery.sizeOf(context).width * 0.82,
        ),
        child: Material(
          color: bubble,
          elevation: 0,
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
                          // The COUNT, and only when there is one. An icon on every post said
                          // "comments are possible here", which is not news — it was on every post
                          // in the channel. A number says somebody replied, which is.
                          if (message.commentCount > 0) ...[
                            Icon(
                              Icons.mode_comment_outlined,
                              size: 13,
                              color: scheme.primary,
                            ),
                            const SizedBox(width: 3),
                            Text(
                              '${message.commentCount}',
                              style: TextStyle(
                                fontSize: 11,
                                color: scheme.primary,
                                fontWeight: FontWeight.w600,
                              ),
                            ),
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
