import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/format.dart';
import '../../core/providers.dart';
import '../../core/snackbar.dart';
import '../../data/app_providers.dart';
import '../../l10n/app_localizations.dart';
import '../../models/models.dart';
import '../../widgets/error_view.dart';

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
    if (event.channelId != widget.channelId) return;
    if (_activeQuery.isNotEmpty) return;
    if (_messages.any((m) => m.id == event.message.id)) return;
    setState(() => _messages = [event.message, ..._messages]);
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;

    // Prepend live messages arriving over SSE.
    ref.listen(liveEventsProvider, (_, next) {
      next.whenData(_onLiveEvent);
    });

    if (_loading) {
      return const Center(child: CircularProgressIndicator());
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
          child: TextField(
            controller: _search,
            textInputAction: TextInputAction.search,
            onSubmitted: (_) => _runSearch(),
            decoration: InputDecoration(
              hintText: l10n.t('channel.searchHint'),
              prefixIcon: const Icon(Icons.search, size: 20),
              suffixIcon: (_search.text.isNotEmpty || _activeQuery.isNotEmpty)
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
    return RefreshIndicator(
      onRefresh: () => _load(query: _activeQuery),
      child: ListView.separated(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
        itemCount: _messages.length + (_cursor.isNotEmpty ? 1 : 0),
        separatorBuilder: (_, _) => const SizedBox(height: 8),
        itemBuilder: (context, i) {
          if (i >= _messages.length) {
            return Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: Center(
                child: _loadingMore
                    ? const SizedBox(
                        height: 24,
                        width: 24,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : OutlinedButton(
                        onPressed: _loadMore,
                        child: Text(l10n.t('channel.loadMore')),
                      ),
              ),
            );
          }
          return _MessageCard(message: _messages[i]);
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
    final scheme = Theme.of(context).colorScheme;
    return Card(
      clipBehavior: Clip.antiAlias,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Images first (Instagram-style), then the text below.
          if (message.images.isNotEmpty) _MessageCarousel(images: message.images),
          Padding(
            padding: const EdgeInsets.all(14),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(
                      child: Text(
                        message.title.isEmpty
                            ? l10n.t('channel.noTitle')
                            : message.title,
                        style: const TextStyle(
                          fontWeight: FontWeight.w600,
                          fontSize: 15,
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Text(
                      formatDateTime(message.createdAt),
                      style: TextStyle(
                        fontSize: 11,
                        color: scheme.onSurfaceVariant,
                      ),
                    ),
                  ],
                ),
                if (message.body.isNotEmpty) ...[
                  const SizedBox(height: 6),
                  Text(message.body, style: const TextStyle(fontSize: 14)),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// An Instagram-style image carousel: a swipeable PageView of cached network
/// images with page-dot indicators. A single image renders without dots.
class _MessageCarousel extends ConsumerStatefulWidget {
  const _MessageCarousel({required this.images});

  final List<MessageImage> images;

  @override
  ConsumerState<_MessageCarousel> createState() => _MessageCarouselState();
}

class _MessageCarouselState extends ConsumerState<_MessageCarousel> {
  final _controller = PageController();
  int _current = 0;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final repo = ref.read(repositoryProvider);
    final scheme = Theme.of(context).colorScheme;
    // Use the first image's aspect ratio for the viewport so single-image posts
    // fit naturally; clamp to avoid extreme tall/wide crops.
    final ratio = widget.images.first.aspectRatio.clamp(0.75, 1.91);

    return Column(
      children: [
        AspectRatio(
          aspectRatio: ratio,
          child: PageView.builder(
            controller: _controller,
            itemCount: widget.images.length,
            onPageChanged: (i) => setState(() => _current = i),
            itemBuilder: (context, i) => CachedNetworkImage(
              imageUrl: repo.imageUrl(widget.images[i].id),
              fit: BoxFit.cover,
              placeholder: (context, _) =>
                  ColoredBox(color: scheme.surfaceContainerHighest),
              errorWidget: (context, _, _) => ColoredBox(
                color: scheme.surfaceContainerHighest,
                child: Icon(Icons.broken_image_outlined, color: scheme.outline),
              ),
            ),
          ),
        ),
        if (widget.images.length > 1)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 8),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                for (var i = 0; i < widget.images.length; i++)
                  Container(
                    width: 6,
                    height: 6,
                    margin: const EdgeInsets.symmetric(horizontal: 3),
                    decoration: BoxDecoration(
                      shape: BoxShape.circle,
                      color: i == _current
                          ? scheme.primary
                          : scheme.outlineVariant,
                    ),
                  ),
              ],
            ),
          ),
      ],
    );
  }
}
