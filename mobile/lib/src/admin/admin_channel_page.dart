import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/snackbar.dart';
import '../l10n/app_localizations.dart';
import '../models/models.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/error_view.dart';
import '../widgets/glass/glass.dart';
import 'admin_providers.dart';
import 'widgets/admin_ui.dart';

/// One channel, read as a moderator: what it has published, and what can publish to it.
///
/// Two lists rather than the web's two tabs. A phone has the room for one column, and a channel
/// with three API keys does not deserve a tab of its own — the keys sit under the messages, where
/// scrolling past them is cheaper than a tab bar is to draw.
class AdminChannelPage extends ConsumerStatefulWidget {
  const AdminChannelPage({super.key, required this.channelId});

  final String channelId;

  @override
  ConsumerState<AdminChannelPage> createState() => _AdminChannelPageState();
}

class _AdminChannelPageState extends ConsumerState<AdminChannelPage> {
  static const _pageSize = 30;

  List<Message> _messages = const [];
  List<ApiKey> _keys = const [];
  String _cursor = '';
  bool _loading = true;
  bool _error = false;
  bool _loadingMore = false;
  String? _busyKeyId;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = false;
    });
    try {
      final admin = ref.read(adminRepositoryProvider);
      // Both at once: they are independent reads and the screen shows them together, so waiting
      // for one before starting the other only makes the page slower to appear.
      final results = await Future.wait([
        admin.channelMessages(widget.channelId, limit: _pageSize),
        admin.channelKeys(widget.channelId),
      ]);
      if (!mounted) return;
      final page = results[0] as MessagesPage;
      setState(() {
        _messages = page.messages;
        _cursor = page.nextCursor;
        _keys = results[1] as List<ApiKey>;
        _loading = false;
      });
    } on Object {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = _messages.isEmpty && _keys.isEmpty;
      });
    }
  }

  Future<void> _loadMore() async {
    if (_cursor.isEmpty || _loadingMore) return;
    setState(() => _loadingMore = true);
    try {
      final page = await ref
          .read(adminRepositoryProvider)
          .channelMessages(widget.channelId, cursor: _cursor, limit: _pageSize);
      if (!mounted) return;
      setState(() {
        _messages = [..._messages, ...page.messages];
        _cursor = page.nextCursor;
        _loadingMore = false;
      });
    } on Object catch (e) {
      if (!mounted) return;
      setState(() => _loadingMore = false);
      notifyError(context, context.l10n.t('admin.loadFailed'), e);
    }
  }

  Future<void> _revokeKey(ApiKey key) async {
    final l10n = context.l10n;
    final ok = await showAdaptiveConfirm(
      context,
      title: l10n.t('admin.revoke'),
      message: l10n
          .t('admin.revokeKeyConfirm')
          .replaceAll('{name}', key.prefix),
      confirmLabel: l10n.t('admin.revoke'),
      cancelLabel: l10n.t('common.cancel'),
      isDestructive: true,
    );
    if (!ok || !mounted) return;

    setState(() => _busyKeyId = key.id);
    try {
      await ref
          .read(adminRepositoryProvider)
          .revokeChannelKey(widget.channelId, key.id);
      if (!mounted) return;
      notifySuccess(context, l10n.t('admin.keyRevoked'));
      await _load();
    } on Object catch (e) {
      if (mounted) notifyError(context, l10n.t('admin.revokeFailed'), e);
    } finally {
      if (mounted) setState(() => _busyKeyId = null);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return AdaptiveScaffold(
      title: Text(l10n.t('admin.channelDetail')),
      behindChrome: true,
      body: _error
          ? ErrorView(message: l10n.t('admin.loadFailed'), onRetry: _load)
          : _loading && _messages.isEmpty && _keys.isEmpty
          ? const Center(child: AdaptiveProgress())
          : AdaptiveRefreshableScrollView(
              onRefresh: _load,
              slivers: [
                SliverToBoxAdapter(
                  child: _Header(title: l10n.t('admin.channelMessages')),
                ),
                if (_messages.isEmpty)
                  SliverToBoxAdapter(child: _Empty(l10n.t('admin.noMessages')))
                else
                  SliverPadding(
                    padding: const EdgeInsets.symmetric(vertical: 4),
                    sliver: SliverList.builder(
                      itemCount: _messages.length,
                      itemBuilder: (context, i) =>
                          _MessageRow(message: _messages[i]),
                    ),
                  ),
                if (_cursor.isNotEmpty)
                  SliverToBoxAdapter(
                    child: Padding(
                      padding: const EdgeInsets.symmetric(vertical: 12),
                      child: Center(
                        child: _loadingMore
                            ? const AdaptiveProgress(size: 20)
                            : AdaptiveButton.text(
                                onPressed: _loadMore,
                                child: Text(l10n.t('admin.loadMore')),
                              ),
                      ),
                    ),
                  ),
                SliverToBoxAdapter(
                  child: _Header(title: l10n.t('admin.channelKeys')),
                ),
                if (_keys.isEmpty)
                  SliverToBoxAdapter(child: _Empty(l10n.t('channel.noKeys')))
                else
                  SliverPadding(
                    padding: const EdgeInsets.symmetric(vertical: 4),
                    sliver: SliverList.builder(
                      itemCount: _keys.length,
                      itemBuilder: (context, i) => _KeyRow(
                        apiKey: _keys[i],
                        busy: _busyKeyId == _keys[i].id,
                        onRevoke: () => _revokeKey(_keys[i]),
                      ),
                    ),
                  ),
              ],
            ),
    );
  }
}

class _MessageRow extends StatelessWidget {
  const _MessageRow({required this.message});

  final Message message;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return ListTile(
      title: Text(
        message.title.isEmpty ? l10n.t('channel.noTitle') : message.title,
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: message.body.isEmpty
          ? null
          : Text(message.body, maxLines: 2, overflow: TextOverflow.ellipsis),
      trailing: Text(
        adminDate(message.createdAt),
        style: Theme.of(context).textTheme.bodySmall,
      ),
    );
  }
}

class _KeyRow extends StatelessWidget {
  const _KeyRow({
    required this.apiKey,
    required this.busy,
    required this.onRevoke,
  });

  final ApiKey apiKey;
  final bool busy;
  final VoidCallback onRevoke;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final theme = Theme.of(context);
    return ListTile(
      title: Text(
        apiKey.label.isEmpty ? apiKey.prefix : apiKey.label,
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 4),
        child: Wrap(
          spacing: 6,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            Text(
              '${apiKey.prefix}…',
              style: theme.textTheme.bodySmall?.copyWith(
                fontFeatures: const [FontFeature.tabularFigures()],
              ),
            ),
            Text(adminDate(apiKey.createdAt), style: theme.textTheme.bodySmall),
            if (apiKey.revoked)
              AdminBadge(
                label: l10n.t('admin.keyRevokedBadge'),
                color: theme.colorScheme.error,
              ),
          ],
        ),
      ),
      trailing: busy
          ? const SizedBox.square(
              dimension: GlassMetrics.minTapTarget,
              child: Center(child: AdaptiveProgress(size: 18)),
            )
          // A revoked key cannot be revoked again, and offering the action anyway is how a list
          // ends up reporting success for something it did not do.
          : apiKey.revoked
          ? null
          : GlassMenuButton(
              semanticLabel: l10n.t('admin.actions'),
              actions: [
                GlassMenuAction(
                  label: l10n.t('admin.revoke'),
                  icon: Icons.block,
                  destructive: true,
                  onSelected: onRevoke,
                ),
              ],
            ),
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.title});

  final String title;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 6),
      child: Text(
        title.toUpperCase(),
        style: theme.textTheme.labelSmall?.copyWith(
          color: theme.colorScheme.onSurfaceVariant,
          letterSpacing: 0.8,
        ),
      ),
    );
  }
}

class _Empty extends StatelessWidget {
  const _Empty(this.label);

  final String label;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 20),
      child: Text(
        label,
        style: theme.textTheme.bodyMedium?.copyWith(
          color: theme.colorScheme.onSurfaceVariant,
        ),
      ),
    );
  }
}
