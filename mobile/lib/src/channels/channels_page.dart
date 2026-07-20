import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/providers.dart';
import '../core/snackbar.dart';
import '../data/app_providers.dart';
import '../l10n/app_localizations.dart';
import '../models/models.dart';
import '../push/push_service.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/brand_logo.dart';
import '../widgets/error_view.dart';
import '../widgets/mode_badge.dart';
import 'create_channel_sheet.dart';
import 'join_channel_sheet.dart';

/// Home screen: the channels the user owns plus those they have joined. Tapping
/// one opens its detail page; the bell action registers this device for push.
class ChannelsPage extends ConsumerWidget {
  const ChannelsPage({super.key});

  Future<void> _enableNotifications(BuildContext context, WidgetRef ref) async {
    final l10n = context.l10n;
    try {
      await ref.read(deviceControllerProvider.notifier).register();
      if (context.mounted) {
        notifySuccess(context, l10n.t('channels.notificationsOn'));
      }
    } on PushUnavailableException catch (e) {
      if (context.mounted) {
        notifyError(context, l10n.t('channels.enableFailed'), e);
      }
    } catch (e) {
      if (context.mounted) {
        notifyError(context, l10n.t('channels.enableFailed'), e);
      }
    }
  }

  Future<void> _create(BuildContext context, WidgetRef ref) async {
    final result =
        await showModalBottomSheet<({String name, SubscriptionMode mode})>(
          context: context,
          isScrollControlled: true,
          builder: (_) => const CreateChannelSheet(),
        );
    if (result == null || !context.mounted) return;
    final l10n = context.l10n;
    try {
      final channel = await ref
          .read(channelsProvider.notifier)
          .create(result.name, result.mode);
      if (context.mounted) {
        notifySuccess(context, l10n.t('channels.created'));
        context.go('/channels/${channel.id}');
      }
    } catch (e) {
      if (context.mounted) {
        notifyError(context, l10n.t('channels.createFailed'), e);
      }
    }
  }

  Future<void> _join(BuildContext context, WidgetRef ref) async {
    final reference = await showModalBottomSheet<String>(
      context: context,
      isScrollControlled: true,
      builder: (_) => const JoinChannelSheet(),
    );
    if (reference == null || reference.isEmpty || !context.mounted) return;
    final l10n = context.l10n;
    try {
      final channel = await ref
          .read(joinedChannelsProvider.notifier)
          .join(reference);
      if (context.mounted) {
        notifySuccess(context, l10n.t('join.joined'));
        context.go('/channels/${channel.id}');
      }
    } catch (e) {
      if (context.mounted) {
        notifyError(context, l10n.t('join.joinFailed'), e);
      }
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = context.l10n;
    final ios = isCupertino(context);
    final channels = ref.watch(channelsProvider);
    final joined = ref.watch(joinedChannelsProvider);
    final registered = ref.watch(deviceControllerProvider) != null;

    final notifIcon = registered
        ? (ios ? CupertinoIcons.bell_fill : Icons.notifications_active_rounded)
        : (ios ? CupertinoIcons.bell : Icons.notifications_none_rounded);

    return AdaptiveScaffold(
      grouped: true,
      title: const BrandLogo(size: 26),
      trailing: [
        AdaptiveIconButton(
          icon: notifIcon,
          semanticLabel: registered
              ? l10n.t('channels.notificationsOn')
              : l10n.t('channels.enableNotifications'),
          onPressed: registered
              ? null
              : () => _enableNotifications(context, ref),
        ),
        AdaptiveIconButton(
          icon: ios
              ? CupertinoIcons.qrcode_viewfinder
              : Icons.qr_code_scanner_rounded,
          semanticLabel: l10n.t('channels.addChannel'),
          onPressed: () => _join(context, ref),
        ),
        AdaptiveIconButton(
          icon: ios ? CupertinoIcons.settings : Icons.settings_outlined,
          semanticLabel: l10n.t('common.settings'),
          onPressed: () => context.push('/settings'),
        ),
        if (ios)
          AdaptiveIconButton(
            icon: CupertinoIcons.add,
            semanticLabel: l10n.t('channels.newChannel'),
            onPressed: () => _create(context, ref),
          ),
      ],
      floatingActionButton: ios
          ? null
          : FloatingActionButton.extended(
              onPressed: () => _create(context, ref),
              icon: const Icon(Icons.add),
              label: Text(l10n.t('channels.newChannel')),
            ),
      body: AdaptiveRefreshableScrollView(
        onRefresh: () => Future.wait([
          ref.read(channelsProvider.notifier).refresh(),
          ref.read(joinedChannelsProvider.notifier).refresh(),
        ]),
        slivers: channels.when(
          loading: () => const [
            SliverFillRemaining(
              hasScrollBody: false,
              child: Center(child: AdaptiveProgress()),
            ),
          ],
          error: (e, _) => [
            SliverFillRemaining(
              hasScrollBody: false,
              child: ErrorView(
                message: l10n.t('channels.loadFailed'),
                onRetry: () => ref.read(channelsProvider.notifier).refresh(),
              ),
            ),
          ],
          data: (owned) {
            final joinedList = joined.asData?.value ?? const <JoinedChannel>[];
            if (owned.isEmpty && joinedList.isEmpty) {
              return [_emptyState(context)];
            }
            return [
              if (owned.isNotEmpty) ..._ownedSlivers(context, owned),
              if (joinedList.isNotEmpty) ..._joinedSlivers(context, joinedList),
              const SliverToBoxAdapter(child: SizedBox(height: 96)),
            ];
          },
        ),
      ),
    );
  }

  Widget _emptyState(BuildContext context) {
    final l10n = context.l10n;
    final theme = Theme.of(context);
    return SliverFillRemaining(
      hasScrollBody: false,
      // Centred, with a heading and a line under it — the same shape the Chats tab uses, because
      // the two tabs are the same screen with different contents and had no business greeting an
      // empty account in two different ways.
      //
      // It used to sit at the bottom, pinned above the button, so the sentence and the button it
      // described were adjacent. That reads well and looks like a different app from the tab next
      // to it; consistency between the two is worth more than the adjacency.
      child: Center(
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                isCupertino(context)
                    ? CupertinoIcons.antenna_radiowaves_left_right
                    : Icons.campaign_outlined,
                size: 44,
                color: theme.colorScheme.onSurfaceVariant,
              ),
              const SizedBox(height: 12),
              Text(
                l10n.t('channels.noChannels'),
                style: theme.textTheme.bodyMedium?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
              const SizedBox(height: 4),
              Text(
                l10n.t('channels.noChannelsHint'),
                textAlign: TextAlign.center,
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  List<Widget> _ownedSlivers(BuildContext context, List<Channel> owned) {
    final l10n = context.l10n;
    if (isCupertino(context)) {
      return [
        SliverToBoxAdapter(
          child: CupertinoListSection.insetGrouped(
            header: Text(l10n.t('channels.ownedSection')),
            margin: const EdgeInsets.fromLTRB(16, 12, 16, 8),
            children: [
              for (final c in owned)
                CupertinoListTile.notched(
                  title: Text(
                    c.name.isEmpty ? l10n.t('channel.fallbackName') : c.name,
                    style: const TextStyle(fontWeight: FontWeight.w600),
                  ),
                  subtitle: Text(c.publicId),
                  trailing: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      ModeBadge(mode: c.subscriptionMode),
                      const SizedBox(width: 8),
                      const CupertinoListTileChevron(),
                    ],
                  ),
                  onTap: () => context.go('/channels/${c.id}'),
                ),
            ],
          ),
        ),
      ];
    }

    return [
      _MaterialSectionHeader(label: l10n.t('channels.ownedSection')),
      SliverPadding(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
        sliver: SliverList.separated(
          itemCount: owned.length,
          separatorBuilder: (_, _) => const SizedBox(height: 10),
          itemBuilder: (context, i) {
            final c = owned[i];
            return _MaterialChannelCard(
              title: c.name.isEmpty ? l10n.t('channel.fallbackName') : c.name,
              subtitle: c.publicId,
              trailing: ModeBadge(mode: c.subscriptionMode),
              onTap: () => context.go('/channels/${c.id}'),
            );
          },
        ),
      ),
    ];
  }

  List<Widget> _joinedSlivers(
    BuildContext context,
    List<JoinedChannel> joined,
  ) {
    final l10n = context.l10n;
    if (isCupertino(context)) {
      return [
        SliverToBoxAdapter(
          child: CupertinoListSection.insetGrouped(
            header: Text(l10n.t('channels.joinedSection')),
            margin: const EdgeInsets.fromLTRB(16, 4, 16, 8),
            children: [
              for (final jc in joined)
                CupertinoListTile.notched(
                  title: Text(
                    jc.channel.name.isEmpty
                        ? l10n.t('channel.fallbackName')
                        : jc.channel.name,
                    style: const TextStyle(fontWeight: FontWeight.w600),
                  ),
                  subtitle: Text(jc.channel.publicId),
                  trailing: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      _RelationIndicator(joined: jc),
                      const SizedBox(width: 8),
                      const CupertinoListTileChevron(),
                    ],
                  ),
                  onTap: () => context.go('/channels/${jc.channel.id}'),
                ),
            ],
          ),
        ),
      ];
    }

    return [
      _MaterialSectionHeader(label: l10n.t('channels.joinedSection')),
      SliverPadding(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
        sliver: SliverList.separated(
          itemCount: joined.length,
          separatorBuilder: (_, _) => const SizedBox(height: 10),
          itemBuilder: (context, i) {
            final jc = joined[i];
            return _MaterialChannelCard(
              title: jc.channel.name.isEmpty
                  ? l10n.t('channel.fallbackName')
                  : jc.channel.name,
              subtitle: jc.channel.publicId,
              trailing: _RelationIndicator(joined: jc),
              onTap: () => context.go('/channels/${jc.channel.id}'),
            );
          },
        ),
      ),
    ];
  }
}

class _MaterialSectionHeader extends StatelessWidget {
  const _MaterialSectionHeader({required this.label});

  final String label;

  @override
  Widget build(BuildContext context) {
    return SliverToBoxAdapter(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 16, 20, 8),
        child: Text(
          label.toUpperCase(),
          style: TextStyle(
            fontSize: 12,
            fontWeight: FontWeight.w700,
            letterSpacing: 0.6,
            color: Theme.of(context).colorScheme.onSurfaceVariant,
          ),
        ),
      ),
    );
  }
}

class _MaterialChannelCard extends StatelessWidget {
  const _MaterialChannelCard({
    required this.title,
    required this.subtitle,
    required this.trailing,
    required this.onTap,
  });

  final String title;
  final String subtitle;
  final Widget trailing;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Card(
      child: ListTile(
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
        title: Text(title, style: const TextStyle(fontWeight: FontWeight.w600)),
        subtitle: Padding(
          padding: const EdgeInsets.only(top: 4),
          child: Text(
            subtitle,
            style: TextStyle(
              color: Theme.of(context).colorScheme.onSurfaceVariant,
              fontSize: 12,
            ),
          ),
        ),
        trailing: trailing,
        onTap: onTap,
      ),
    );
  }
}

/// Role/status pill(s) for a joined channel: an admin badge and/or a
/// pending/blocked status badge (active members show nothing).
class _RelationIndicator extends StatelessWidget {
  const _RelationIndicator({required this.joined});

  final JoinedChannel joined;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final scheme = Theme.of(context).colorScheme;
    final pills = <Widget>[
      if (joined.role == ChannelRole.admin)
        _Pill(label: l10n.t('channel.roleAdmin'), color: scheme.primary),
      if (joined.memberStatus == MemberStatus.pending)
        _Pill(label: l10n.t('channel.statusPending'), color: scheme.secondary),
      if (joined.memberStatus == MemberStatus.blocked)
        _Pill(label: l10n.t('channel.statusBlocked'), color: scheme.error),
    ];
    if (pills.isEmpty) return const SizedBox.shrink();
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        for (var i = 0; i < pills.length; i++) ...[
          if (i > 0) const SizedBox(width: 6),
          pills[i],
        ],
        const SizedBox(width: 6),
      ],
    );
  }
}

class _Pill extends StatelessWidget {
  const _Pill({required this.label, required this.color});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(isCupertino(context) ? 100 : 8),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
