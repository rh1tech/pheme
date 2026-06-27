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

/// Home screen: the authenticated user's channels. Tapping one opens its detail
/// page; the bell action registers this device for push notifications.
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

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = context.l10n;
    final ios = isCupertino(context);
    final channels = ref.watch(channelsProvider);
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
        onRefresh: () => ref.read(channelsProvider.notifier).refresh(),
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
          data: (list) => _channelSlivers(context, list),
        ),
      ),
    );
  }

  List<Widget> _channelSlivers(BuildContext context, List<Channel> channels) {
    final l10n = context.l10n;
    if (channels.isEmpty) {
      return [
        SliverFillRemaining(
          hasScrollBody: false,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(24, 96, 24, 24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  isCupertino(context)
                      ? CupertinoIcons.antenna_radiowaves_left_right
                      : Icons.campaign_outlined,
                  size: 48,
                  color: Theme.of(context).colorScheme.outline,
                ),
                const SizedBox(height: 12),
                Text(
                  l10n.t('channels.noChannels'),
                  textAlign: TextAlign.center,
                  style: TextStyle(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                ),
              ],
            ),
          ),
        ),
      ];
    }

    if (isCupertino(context)) {
      return [
        SliverToBoxAdapter(
          child: CupertinoListSection.insetGrouped(
            margin: const EdgeInsets.fromLTRB(16, 12, 16, 24),
            children: [
              for (final c in channels)
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
      SliverPadding(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 96),
        sliver: SliverList.separated(
          itemCount: channels.length,
          separatorBuilder: (_, _) => const SizedBox(height: 10),
          itemBuilder: (context, i) {
            final c = channels[i];
            return Card(
              child: ListTile(
                contentPadding: const EdgeInsets.symmetric(
                  horizontal: 16,
                  vertical: 6,
                ),
                title: Text(
                  c.name.isEmpty ? l10n.t('channel.fallbackName') : c.name,
                  style: const TextStyle(fontWeight: FontWeight.w600),
                ),
                subtitle: Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: Text(
                    c.publicId,
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                      fontSize: 12,
                    ),
                  ),
                ),
                trailing: ModeBadge(mode: c.subscriptionMode),
                onTap: () => context.go('/channels/${c.id}'),
              ),
            );
          },
        ),
      ),
    ];
  }
}
