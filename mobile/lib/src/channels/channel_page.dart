import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/snackbar.dart';
import '../data/app_providers.dart';
import '../l10n/app_localizations.dart';
import '../models/models.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/mode_badge.dart';
import 'tabs/channel_settings_tab.dart';
import 'tabs/keys_tab.dart';
import 'tabs/messages_tab.dart';
import 'tabs/send_tab.dart';

/// Channel detail: a header with the trigger ID plus access to message history,
/// sending, API keys and settings. iOS presents the four sections as a native
/// bottom tab bar; Android uses a Material top tab bar.
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
      return const AdaptiveScaffold(body: Center(child: AdaptiveProgress()));
    }
    if (channel == null) {
      return AdaptiveScaffold(
        body: Center(child: Text(l10n.t('channel.notFound'))),
      );
    }

    return isCupertino(context)
        ? _CupertinoChannelView(channelId: channelId, channel: channel)
        : _MaterialChannelView(channelId: channelId, channel: channel);
  }
}

/// Android: Material app bar + top tab bar.
class _MaterialChannelView extends StatelessWidget {
  const _MaterialChannelView({required this.channelId, required this.channel});

  final String channelId;
  final Channel channel;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
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

/// iOS: Cupertino nav bar + a bottom tab bar switching the four sections via an
/// [IndexedStack] (so tab state is preserved and go_router's back chevron keeps
/// working, unlike a nested CupertinoTabScaffold navigator).
class _CupertinoChannelView extends StatefulWidget {
  const _CupertinoChannelView({required this.channelId, required this.channel});

  final String channelId;
  final Channel channel;

  @override
  State<_CupertinoChannelView> createState() => _CupertinoChannelViewState();
}

class _CupertinoChannelViewState extends State<_CupertinoChannelView> {
  int _index = 0;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final channel = widget.channel;
    return CupertinoPageScaffold(
      navigationBar: CupertinoNavigationBar(
        middle: Text(
          channel.name.isEmpty ? l10n.t('channel.fallbackName') : channel.name,
        ),
        trailing: ModeBadge(mode: channel.subscriptionMode),
        backgroundColor: CupertinoTheme.of(context).scaffoldBackgroundColor,
      ),
      // CupertinoPageScaffold under MaterialApp supplies no DefaultTextStyle, so
      // seed the Cupertino label style for the tab bodies' raw Text widgets.
      child: DefaultTextStyle(
        style: CupertinoTheme.of(context).textTheme.textStyle,
        child: SafeArea(
          top: false,
          child: Column(
            children: [
              _TriggerIdBar(channel: channel),
              Expanded(
                child: IndexedStack(
                  index: _index,
                  children: [
                    MessagesTab(channelId: widget.channelId),
                    SendTab(channelId: widget.channelId),
                    KeysTab(channelId: widget.channelId),
                    ChannelSettingsTab(channel: channel),
                  ],
                ),
              ),
              CupertinoTabBar(
                currentIndex: _index,
                onTap: (i) => setState(() => _index = i),
                items: [
                  BottomNavigationBarItem(
                    icon: const Icon(CupertinoIcons.bubble_left_bubble_right),
                    label: l10n.t('channel.tabMessages'),
                  ),
                  BottomNavigationBarItem(
                    icon: const Icon(CupertinoIcons.paperplane),
                    label: l10n.t('channel.tabSend'),
                  ),
                  BottomNavigationBarItem(
                    icon: const Icon(CupertinoIcons.lock),
                    label: l10n.t('channel.tabKeys'),
                  ),
                  BottomNavigationBarItem(
                    icon: const Icon(CupertinoIcons.gear),
                    label: l10n.t('channel.tabSettings'),
                  ),
                ],
              ),
            ],
          ),
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
      padding: const EdgeInsets.fromLTRB(16, 8, 8, 8),
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
          AdaptiveIconButton(
            icon: isCupertino(context) ? CupertinoIcons.doc_on_doc : Icons.copy,
            semanticLabel: l10n.t('common.copy'),
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
