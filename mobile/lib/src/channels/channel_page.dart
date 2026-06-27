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
import '../widgets/error_view.dart';
import '../widgets/mode_badge.dart';
import 'tabs/channel_settings_tab.dart';
import 'tabs/keys_tab.dart';
import 'tabs/messages_tab.dart';
import 'tabs/send_tab.dart';
import 'tabs/subscribers_tab.dart';

/// One section of the channel detail's tab bar.
class _ChannelTab {
  const _ChannelTab({
    required this.label,
    required this.materialIcon,
    required this.cupertinoIcon,
    required this.body,
  });

  final String label;
  final IconData materialIcon;
  final IconData cupertinoIcon;
  final Widget body;
}

/// Channel detail: a header with the trigger ID plus the sections the caller is
/// entitled to. Everyone sees Messages and Settings; owners and admins also see
/// Send and Subscribers; only the owner sees Keys. iOS presents the sections as
/// a native bottom tab bar; Android uses a Material top tab bar.
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
      data: (rel) {
        final tabs = _tabs(context, rel);
        return isCupertino(context)
            ? _CupertinoChannelView(channel: rel.channel, tabs: tabs)
            : _MaterialChannelView(channel: rel.channel, tabs: tabs);
      },
    );
  }

  List<_ChannelTab> _tabs(BuildContext context, ChannelRelation rel) {
    final l10n = context.l10n;
    return [
      _ChannelTab(
        label: l10n.t('channel.tabMessages'),
        materialIcon: Icons.forum_outlined,
        cupertinoIcon: CupertinoIcons.bubble_left_bubble_right,
        body: MessagesTab(channelId: channelId),
      ),
      if (rel.canManage)
        _ChannelTab(
          label: l10n.t('channel.tabSend'),
          materialIcon: Icons.send_outlined,
          cupertinoIcon: CupertinoIcons.paperplane,
          body: SendTab(channelId: channelId),
        ),
      if (rel.isOwner)
        _ChannelTab(
          label: l10n.t('channel.tabKeys'),
          materialIcon: Icons.key_outlined,
          cupertinoIcon: CupertinoIcons.lock,
          body: KeysTab(channelId: channelId),
        ),
      if (rel.canManage)
        _ChannelTab(
          label: l10n.t('channel.tabSubscribers'),
          materialIcon: Icons.group_outlined,
          cupertinoIcon: CupertinoIcons.person_2,
          body: SubscribersTab(channelId: channelId),
        ),
      _ChannelTab(
        label: l10n.t('channel.tabSettings'),
        materialIcon: Icons.settings_outlined,
        cupertinoIcon: CupertinoIcons.gear,
        body: ChannelSettingsTab(relation: rel),
      ),
    ];
  }
}

/// Android: Material app bar + top tab bar.
class _MaterialChannelView extends StatelessWidget {
  const _MaterialChannelView({required this.channel, required this.tabs});

  final Channel channel;
  final List<_ChannelTab> tabs;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return DefaultTabController(
      length: tabs.length,
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
                  tabs: [for (final t in tabs) Tab(text: t.label)],
                ),
              ],
            ),
          ),
        ),
        body: TabBarView(children: [for (final t in tabs) t.body]),
      ),
    );
  }
}

/// iOS: Cupertino nav bar + a bottom tab bar switching the sections via an
/// [IndexedStack] (so tab state is preserved and go_router's back chevron keeps
/// working, unlike a nested CupertinoTabScaffold navigator).
class _CupertinoChannelView extends StatefulWidget {
  const _CupertinoChannelView({required this.channel, required this.tabs});

  final Channel channel;
  final List<_ChannelTab> tabs;

  @override
  State<_CupertinoChannelView> createState() => _CupertinoChannelViewState();
}

class _CupertinoChannelViewState extends State<_CupertinoChannelView> {
  int _index = 0;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final channel = widget.channel;
    final tabs = widget.tabs;
    // Guard against an out-of-range index if the tab set shrinks on reload.
    final index = _index.clamp(0, tabs.length - 1);
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
                  index: index,
                  children: [for (final t in tabs) t.body],
                ),
              ),
              CupertinoTabBar(
                currentIndex: index,
                onTap: (i) => setState(() => _index = i),
                items: [
                  for (final t in tabs)
                    BottomNavigationBarItem(
                      icon: Icon(t.cupertinoIcon),
                      label: t.label,
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
