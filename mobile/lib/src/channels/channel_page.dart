import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../data/app_providers.dart';
import '../chat/widgets/chat_wallpaper.dart';
import '../chat/widgets/conversation_avatar.dart';
import '../core/providers.dart';
import '../core/snackbar.dart';
import '../l10n/app_localizations.dart';
import '../models/models.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/error_view.dart';
import '../widgets/measured_height.dart';
import '../widgets/glass/glass.dart';
import 'channel_action_sheets.dart';
import 'channel_sheets.dart';
import 'widgets/channel_composer.dart';
import 'tabs/keys_tab.dart';
import 'tabs/messages_tab.dart';
import 'tabs/subscribers_tab.dart';

/// A channel, as one screen.
///
/// It used to be a five-tab container — Messages, Send, Keys, Subscribers, Settings — where a chat
/// is a single page with its actions behind a ⋮ menu. Four of those tabs were things you do to a
/// channel occasionally and one was the channel itself, all given equal billing, so opening a
/// channel put the reason you opened it behind a row of things you mostly did not want. Two of them
/// existed twice over besides, once for Material and once for Cupertino.
///
/// Now it opens on its messages, like a chat does. Posting is a button rather than a tab, the way
/// starting a chat is a button rather than a tab. Everything else lives in the menu, shown only to
/// the roles that may act on it — the same arrangement the web moved to, and the same sections in
/// the same order.
class ChannelPage extends ConsumerWidget {
  const ChannelPage({super.key, required this.channelId});

  final String channelId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = context.l10n;
    final relation = ref.watch(channelRelationProvider(channelId));

    return relation.when(
      loading: () =>
          const AdaptiveScaffold(body: Center(child: AdaptiveProgress())),
      error: (e, _) => AdaptiveScaffold(
        body: ErrorView(
          message: l10n.t('channel.notFound'),
          onRetry: () => ref.invalidate(channelRelationProvider(channelId)),
        ),
      ),
      data: (rel) => _ChannelView(relation: rel),
    );
  }
}

class _ChannelView extends ConsumerStatefulWidget {
  const _ChannelView({required this.relation});

  final ChannelRelation relation;

  @override
  ConsumerState<_ChannelView> createState() => _ChannelViewState();
}

class _ChannelViewState extends ConsumerState<_ChannelView> {
  /// Measured rather than assumed: the composer grows with attached images and with a post long
  /// enough to wrap, and the feed underneath has to know how much of itself is covered.
  double _composerHeight = 0;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final relation = widget.relation;
    final channel = relation.channel;

    return AdaptiveScaffold(
      // The feed runs the full height of the screen, under the bar and under the composer — the
      // same arrangement a chat has, and the reason either of them is worth blurring.
      behindChrome: true,
      title: Row(
        children: [
          ConversationAvatar(
            id: channel.id,
            label: channel.name,
            size: 32,
            imageUrl: channel.avatarId == null
                ? null
                : ref.read(repositoryProvider).imageUrl(channel.avatarId!),
          ),
          const SizedBox(width: GlassMetrics.gap),
          Expanded(child: Text(channel.name, overflow: TextOverflow.ellipsis)),
        ],
      ),
      // The same reason the chat header is leading-aligned on both platforms: this title is an
      // avatar and a name, not a word.
      centerTitle: false,
      trailing: [
        // Everything that is not "read the channel". Only the entries this reader may act on are
        // built, so a plain subscriber sees a short menu rather than a long one full of refusals.
        GlassMenuButton(
          semanticLabel: l10n.t('channel.menu'),
          actions: [
            // Changing what the channel IS — its name, its phetag, who may join. Owner only, as
            // before: an admin manages the channel's people, not its identity.
            if (relation.isOwner)
              GlassMenuAction(
                label: l10n.t('channel.tabSettings'),
                icon: Icons.tune,
                onSelected: () =>
                    context.push('/channels/${relation.channel.id}/settings'),
              ),
            // Everybody, including a plain reader: this is about THIS device being woken, which is
            // the one channel setting that belongs to the person rather than the channel.
            GlassMenuAction(
              label: l10n.t('channel.subscribeTitle'),
              icon: Icons.notifications_outlined,
              onSelected: () =>
                  showChannelNotificationsSheet(context, relation.channel.id),
            ),
            if (relation.canManage)
              GlassMenuAction(
                label: l10n.t('channel.tabSubscribers'),
                icon: Icons.group_outlined,
                onSelected: () => showChannelSheet<void>(
                  context,
                  title: l10n.t('channel.tabSubscribers'),
                  fill: true,
                  child: SubscribersTab(channelId: relation.channel.id),
                ),
              ),
            if (relation.isOwner)
              GlassMenuAction(
                label: l10n.t('channel.tabKeys'),
                icon: Icons.key_outlined,
                onSelected: () => showChannelSheet<void>(
                  context,
                  title: l10n.t('channel.tabKeys'),
                  fill: true,
                  child: KeysTab(channelId: relation.channel.id),
                ),
              ),
            // The join reference is public by nature — it is how anybody joins — so this is the
            // "hand the channel on" action, and it belongs to whoever manages the channel.
            if (relation.canManage)
              GlassMenuAction(
                label: l10n.t('channel.shareTitle'),
                icon: Icons.qr_code_2_rounded,
                onSelected: () =>
                    showChannelShareSheet(context, relation.channel),
              ),
            // Last, and destructive. Two different acts wearing one slot: an owner ends the channel
            // for everybody, and anybody else only walks away from it.
            GlassMenuAction(
              label: relation.isOwner
                  ? l10n.t('channel.dangerTitle')
                  : l10n.t('channel.leaveAction'),
              icon: relation.isOwner ? Icons.delete_outline : Icons.logout,
              destructive: true,
              onSelected: () => relation.isOwner
                  ? _confirmDelete(context, ref, relation.channel)
                  : _confirmLeave(context, ref, relation.channel),
            ),
          ],
        ),
      ],
      // Edge to edge, behind both bars: a glass bar with nothing but a flat colour behind it is
      // just a translucent rectangle.
      body: ChatWallpaper(
        child: Stack(
          children: [
            Positioned.fill(
              child: MessagesTab(
                channelId: channel.id,
                // A reader has no composer, so what the feed has to clear at the foot of the
                // screen is the home indicator instead.
                bottomInset: relation.canManage
                    ? _composerHeight + GlassMetrics.gap
                    : MediaQuery.paddingOf(context).bottom + GlassMetrics.gap,
              ),
            ),
            // Where a chat keeps its message box. Only for those who may post — a channel is a
            // broadcast, and a reader who cannot post should not be shown a box that will refuse
            // them.
            if (relation.canManage)
              Positioned(
                left: 0,
                right: 0,
                bottom: 0,
                child: MeasuredHeight(
                  onChange: (h) {
                    if (mounted) setState(() => _composerHeight = h);
                  },
                  child: ChannelComposer(
                    channelId: channel.id,
                    // Nothing to refresh: the feed already prepends the post when it comes back
                    // down the live stream, and it de-duplicates by id — reloading here would race
                    // that and could show the post twice.
                    onSent: () {},
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

/// Ends the channel for everyone. Owner only, and confirmed by name.
Future<void> _confirmDelete(
  BuildContext context,
  WidgetRef ref,
  Channel channel,
) async {
  final l10n = context.l10n;
  final confirmed = await showAdaptiveConfirm(
    context,
    title: l10n.t('channel.dangerTitle'),
    message: l10n.tp('channel.deleteConfirm', {'name': channel.name}),
    confirmLabel: l10n.t('common.delete'),
    cancelLabel: l10n.t('common.cancel'),
    isDestructive: true,
  );
  if (!confirmed || !context.mounted) return;

  try {
    await ref.read(repositoryProvider).deleteChannel(channel.id);
    await ref.read(channelsProvider.notifier).refresh();
    if (!context.mounted) return;
    notifySuccess(context, l10n.t('channel.channelDeleted'));
    // Home, because the screen this was pressed on no longer describes anything.
    context.go('/');
  } catch (e) {
    if (context.mounted) {
      notifyError(context, l10n.t('channel.deleteFailed'), e);
    }
  }
}

/// Walks away from a channel somebody else owns.
Future<void> _confirmLeave(
  BuildContext context,
  WidgetRef ref,
  Channel channel,
) async {
  final l10n = context.l10n;
  final confirmed = await showAdaptiveConfirm(
    context,
    title: l10n.t('channel.leaveTitle'),
    message: l10n.tp('channel.leaveConfirm', {'name': channel.name}),
    confirmLabel: l10n.t('channel.leaveAction'),
    cancelLabel: l10n.t('common.cancel'),
    isDestructive: true,
  );
  if (!confirmed || !context.mounted) return;

  try {
    await ref.read(repositoryProvider).leaveChannel(channel.id);
    await ref.read(joinedChannelsProvider.notifier).refresh();
    if (!context.mounted) return;
    notifySuccess(context, l10n.t('channel.left'));
    context.go('/');
  } catch (e) {
    if (context.mounted) notifyError(context, l10n.t('channel.leaveFailed'), e);
  }
}
