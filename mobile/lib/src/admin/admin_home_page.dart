import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../l10n/app_localizations.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/error_view.dart';
import 'admin_models.dart';
import 'admin_providers.dart';
import 'widgets/admin_ui.dart';

/// The admin panel's front door: the system's figures, and the way into each section.
///
/// The web puts the sections in a permanent sidebar. A phone has no sidebar, and a tab bar would
/// put six peers where the app already spends its one tab bar on Chats and Channels — so the
/// sections are rows on this page, reached and left the way every other screen here is.
class AdminHomePage extends ConsumerStatefulWidget {
  const AdminHomePage({super.key});

  @override
  ConsumerState<AdminHomePage> createState() => _AdminHomePageState();
}

class _AdminHomePageState extends ConsumerState<AdminHomePage> {
  AdminStats? _stats;
  bool _loading = true;
  bool _error = false;

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
      final stats = await ref.read(adminRepositoryProvider).stats();
      if (!mounted) return;
      setState(() {
        _stats = stats;
        _loading = false;
      });
    } on Object {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = _stats == null;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return AdaptiveScaffold(
      title: Text(l10n.t('admin.title')),
      behindChrome: true,
      body: _error
          ? ErrorView(message: l10n.t('admin.loadFailed'), onRetry: _load)
          : AdaptiveRefreshableScrollView(
              onRefresh: _load,
              slivers: [
                SliverToBoxAdapter(
                  child: _stats == null && _loading
                      ? const Padding(
                          padding: EdgeInsets.symmetric(vertical: 48),
                          child: Center(child: AdaptiveProgress()),
                        )
                      : _buildBody(context, l10n),
                ),
              ],
            ),
    );
  }

  Widget _buildBody(BuildContext context, AppLocalizations l10n) {
    final stats = _stats;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (stats != null) ...[
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
            child: _StatGrid(stats: stats, l10n: l10n),
          ),
          if (stats.topChannels.isNotEmpty) ...[
            _SectionHeader(title: l10n.t('admin.topChannels')),
            for (final c in stats.topChannels)
              ListTile(
                title: Text(
                  c.name,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                trailing: Text(
                  l10n
                      .t('admin.messagesCount')
                      .replaceAll('{count}', '${c.count}'),
                  style: Theme.of(context).textTheme.bodySmall,
                ),
                onTap: () => context.push('/admin/channels/${c.channelId}'),
              ),
          ],
          if (stats.recentMessages.isNotEmpty) ...[
            _SectionHeader(title: l10n.t('admin.recentMessages')),
            for (final m in stats.recentMessages)
              ListTile(
                title: Text(
                  m.title.isEmpty ? l10n.t('channel.noTitle') : m.title,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                subtitle: Text(
                  m.body,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                trailing: Text(
                  adminDate(m.createdAt),
                  style: Theme.of(context).textTheme.bodySmall,
                ),
                onTap: () => context.push('/admin/channels/${m.channelId}'),
              ),
          ],
        ],
        _SectionHeader(title: l10n.t('admin.sections')),
        _SectionRow(
          icon: Icons.people_outline,
          label: l10n.t('admin.navUsers'),
          onTap: () => context.push('/admin/users'),
        ),
        _SectionRow(
          icon: Icons.campaign_outlined,
          label: l10n.t('admin.navChannels'),
          onTap: () => context.push('/admin/channels'),
        ),
        _SectionRow(
          icon: Icons.mode_comment_outlined,
          label: l10n.t('admin.navComments'),
          onTap: () => context.push('/admin/comments'),
        ),
        _SectionRow(
          icon: Icons.confirmation_number_outlined,
          label: l10n.t('admin.navInvites'),
          onTap: () => context.push('/admin/invites'),
        ),
      ],
    );
  }
}

/// The five totals. A wrap rather than a fixed grid, so three tiles fit on a wide phone and two on
/// a narrow one without either being told about the screen.
class _StatGrid extends StatelessWidget {
  const _StatGrid({required this.stats, required this.l10n});

  final AdminStats stats;
  final AppLocalizations l10n;

  @override
  Widget build(BuildContext context) {
    final tiles = <(String, int)>[
      (l10n.t('admin.statUsers'), stats.users),
      (l10n.t('admin.statChannels'), stats.channels),
      (l10n.t('admin.statMessages'), stats.messages),
      (l10n.t('admin.statDeliveries'), stats.deliveries),
      (l10n.t('admin.statDevices'), stats.devices),
    ];
    return LayoutBuilder(
      builder: (context, constraints) {
        const gap = 8.0;
        final columns = constraints.maxWidth >= 420 ? 3 : 2;
        final width = (constraints.maxWidth - gap * (columns - 1)) / columns;
        return Wrap(
          spacing: gap,
          runSpacing: gap,
          children: [
            for (final (label, value) in tiles)
              SizedBox(
                width: width,
                child: AdminStatTile(label: label, value: value),
              ),
          ],
        );
      },
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.title});

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

class _SectionRow extends StatelessWidget {
  const _SectionRow({
    required this.icon,
    required this.label,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      leading: Icon(icon),
      title: Text(label),
      trailing: const Icon(Icons.chevron_right, size: 20),
      onTap: onTap,
    );
  }
}
