import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/snackbar.dart';
import '../data/app_providers.dart';
import '../l10n/app_localizations.dart';
import '../models/models.dart';
import '../widgets/mode_badge.dart';
import 'tabs/channel_settings_tab.dart';
import 'tabs/keys_tab.dart';
import 'tabs/messages_tab.dart';
import 'tabs/send_tab.dart';

/// Channel detail: a header with the trigger ID plus tabbed access to message
/// history, sending, API keys and settings.
class ChannelPage extends ConsumerWidget {
  const ChannelPage({super.key, required this.channelId});

  final String channelId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = context.l10n;
    final channels = ref.watch(channelsProvider);
    final channel = ref.watch(channelProvider(channelId));

    // Still loading the channel list on a cold deep-link.
    if (channel == null && channels.isLoading) {
      return Scaffold(
        appBar: AppBar(),
        body: const Center(child: CircularProgressIndicator()),
      );
    }
    if (channel == null) {
      return Scaffold(
        appBar: AppBar(),
        body: Center(child: Text(l10n.t('channel.notFound'))),
      );
    }

    return DefaultTabController(
      length: 4,
      child: Scaffold(
        appBar: AppBar(
          leading: IconButton(
            icon: const Icon(Icons.arrow_back),
            onPressed: () => context.canPop() ? context.pop() : context.go('/'),
          ),
          title: Text(
            channel.name.isEmpty
                ? l10n.t('channel.fallbackName')
                : channel.name,
          ),
          actions: [
            ModeBadge(mode: channel.subscriptionMode),
            const SizedBox(width: 12),
          ],
          bottom: PreferredSize(
            preferredSize: const Size.fromHeight(96),
            child: Column(
              children: [
                _TriggerIdBar(channel: channel),
                TabBar(
                  isScrollable: true,
                  tabAlignment: TabAlignment.start,
                  tabs: [
                    Tab(text: l10n.t('channel.tabMessages')),
                    Tab(text: l10n.t('channel.tabSend')),
                    Tab(text: l10n.t('channel.tabKeys')),
                    Tab(text: l10n.t('channel.tabSettings')),
                  ],
                ),
              ],
            ),
          ),
        ),
        body: TabBarView(
          children: [
            MessagesTab(channelId: channelId),
            SendTab(channelId: channelId),
            KeysTab(channelId: channelId),
            ChannelSettingsTab(channel: channel),
          ],
        ),
      ),
    );
  }
}

class _TriggerIdBar extends StatelessWidget {
  const _TriggerIdBar({required this.channel});

  final Channel channel;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final scheme = Theme.of(context).colorScheme;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 0, 8, 8),
      child: Row(
        children: [
          Text(
            '${l10n.t('channel.triggerId')}: ',
            style: TextStyle(fontSize: 12, color: scheme.onSurfaceVariant),
          ),
          Flexible(
            child: Text(
              channel.publicId,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                fontFamily: 'monospace',
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          IconButton(
            visualDensity: VisualDensity.compact,
            tooltip: l10n.t('common.copy'),
            icon: const Icon(Icons.copy, size: 16),
            onPressed: () async {
              await Clipboard.setData(ClipboardData(text: channel.publicId));
              if (context.mounted) {
                notifySuccess(context, l10n.t('channel.idCopied'));
              }
            },
          ),
        ],
      ),
    );
  }
}
