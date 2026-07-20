import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/app_providers.dart';
import '../chat/widgets/conversation_avatar.dart';
import '../core/providers.dart';
import '../l10n/app_localizations.dart';
import '../models/models.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/error_view.dart';
import 'channel_sheets.dart';
import 'widgets/channel_composer.dart';
import 'tabs/channel_settings_tab.dart';
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

class _ChannelView extends ConsumerWidget {
  const _ChannelView({required this.relation});

  final ChannelRelation relation;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = context.l10n;
    final channel = relation.channel;

    return AdaptiveScaffold(
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
          const SizedBox(width: 10),
          Expanded(child: Text(channel.name, overflow: TextOverflow.ellipsis)),
        ],
      ),
      trailing: [
        // Everything that is not "read the channel". Only the entries this reader may act on are
        // built, so a plain subscriber sees a short menu rather than a long one full of refusals.
        PopupMenuButton<String>(
          icon: const Icon(Icons.more_vert),
          tooltip: l10n.t('channel.menu'),
          onSelected: (value) => _onMenu(context, value),
          itemBuilder: (context) => [
            PopupMenuItem(
              value: 'info',
              child: Text(l10n.t('channel.tabSettings')),
            ),
            if (relation.canManage)
              PopupMenuItem(
                value: 'subscribers',
                child: Text(l10n.t('channel.tabSubscribers')),
              ),
            if (relation.isOwner)
              PopupMenuItem(
                value: 'keys',
                child: Text(l10n.t('channel.tabKeys')),
              ),
          ],
        ),
      ],
      body: Column(
        children: [
          Expanded(child: MessagesTab(channelId: channel.id)),
          // Where a chat keeps its message box. Only for those who may post — a channel is a
          // broadcast, and a reader who cannot post should not be shown a box that will refuse
          // them.
          if (relation.canManage)
            ChannelComposer(
              channelId: channel.id,
              // Nothing to refresh: the feed already prepends the post when it comes back down the
              // live stream, and it de-duplicates by id — reloading here would race that and could
              // show the post twice.
              onSent: () {},
            ),
        ],
      ),
    );
  }

  void _onMenu(BuildContext context, String value) {
    final l10n = context.l10n;
    switch (value) {
      case 'info':
        showChannelSheet<void>(
          context,
          title: l10n.t('channel.tabSettings'),
          child: ChannelSettingsTab(relation: relation),
        );
      case 'subscribers':
        showChannelSheet<void>(
          context,
          title: l10n.t('channel.tabSubscribers'),
          child: SubscribersTab(channelId: relation.channel.id),
        );
      case 'keys':
        showChannelSheet<void>(
          context,
          title: l10n.t('channel.tabKeys'),
          child: KeysTab(channelId: relation.channel.id),
        );
    }
  }
}
