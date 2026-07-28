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
import '../widgets/glass/glass.dart';
import 'widgets/channel_row.dart';
import '../widgets/error_view.dart';
import '../widgets/pinned_search_header.dart';
import 'create_channel_sheet.dart';
import 'join_channel_sheet.dart';

/// Home screen: the channels the user owns plus those they have joined. Tapping
/// one opens its detail page; the bell action registers this device for push.
class ChannelsPage extends ConsumerStatefulWidget {
  const ChannelsPage({super.key});

  @override
  ConsumerState<ChannelsPage> createState() => _ChannelsPageState();
}

class _ChannelsPageState extends ConsumerState<ChannelsPage> {
  final _search = TextEditingController();
  String _query = '';

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  /// Filters on the channel name, which is what the row shows and therefore the only thing a
  /// reader can be looking for. Client-side against the already-loaded list: this endpoint returns
  /// the channels you own and the ones you have joined, with no filter and no paging, so there is
  /// nothing to ask the server for that it has not already sent.
  List<Channel> _filterOwned(List<Channel> all) {
    if (_query.isEmpty) return all;
    final needle = _query.toLowerCase();
    return all
        .where((c) => c.name.toLowerCase().contains(needle))
        .toList(growable: false);
  }

  List<JoinedChannel> _filterJoined(List<JoinedChannel> all) {
    if (_query.isEmpty) return all;
    final needle = _query.toLowerCase();
    return all
        .where((j) => j.channel.name.toLowerCase().contains(needle))
        .toList(growable: false);
  }

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
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final ios = isCupertino(context);
    final channels = ref.watch(channelsProvider);
    final joined = ref.watch(joinedChannelsProvider);
    final registered = ref.watch(deviceControllerProvider) != null;

    final notifIcon = registered
        ? (ios ? CupertinoIcons.bell_fill : Icons.notifications_active_rounded)
        : (ios ? CupertinoIcons.bell : Icons.notifications_none_rounded);

    // Hidden when there is nothing to search, and kept while a search is running however empty its
    // result — the same rule the Chats tab follows, for the same reasons.
    final searchable = _hasAnyChannel(channels, joined) || _query.isNotEmpty;

    return AdaptiveScaffold(
      behindChrome: true,
      grouped: true,
      title: const BrandLogo(size: 26),
      // Leading on both, for the reason the Chats tab gives: this is a brand mark on a home screen,
      // not the name of a pushed page.
      centerTitle: false,
      trailing: [
        GlassIconButton(
          icon: notifIcon,
          semanticLabel: registered
              ? l10n.t('channels.notificationsOn')
              : l10n.t('channels.enableNotifications'),
          onPressed: registered
              ? null
              : () => _enableNotifications(context, ref),
        ),
        GlassIconButton(
          icon: ios
              ? CupertinoIcons.qrcode_viewfinder
              : Icons.qr_code_scanner_rounded,
          semanticLabel: l10n.t('channels.addChannel'),
          onPressed: () => _join(context, ref),
        ),
        GlassIconButton(
          icon: ios ? CupertinoIcons.settings : Icons.settings_outlined,
          semanticLabel: l10n.t('common.settings'),
          onPressed: () => context.push('/settings'),
        ),
        // Last on the bar on iOS, a floating button on Android — the same split the Chats tab makes,
        // and for the same reason.
        if (ios)
          GlassIconButton(
            icon: CupertinoIcons.add,
            semanticLabel: l10n.t('channels.newChannel'),
            onPressed: () => _create(context, ref),
          ),
      ],
      floatingActionButton: ios
          ? null
          : GlassActionButton(
              icon: Icons.add,
              semanticLabel: l10n.t('channels.newChannel'),
              onPressed: () => _create(context, ref),
            ),
      body: Builder(
        builder: (context) {
          final media = MediaQuery.of(context);
          // Same arrangement as Chats: the field is pinned outside the scroll view, and the list is
          // told to begin below it as top padding.
          final feed = MediaQuery(
            data: media.copyWith(
              padding: media.padding.copyWith(
                top:
                    media.padding.top +
                    (searchable ? PinnedSearchHeader.extent : 0),
              ),
            ),
            child: _feed(context, ref, l10n, channels, joined),
          );

          if (!searchable) return feed;

          return Stack(
            children: [
              Positioned.fill(child: feed),
              Positioned(
                top: media.padding.top,
                left: 0,
                right: 0,
                child: PinnedSearchHeader(
                  controller: _search,
                  placeholder: l10n.t('channels.search'),
                  onChanged: (v) => setState(() => _query = v.trim()),
                ),
              ),
            ],
          );
        },
      ),
    );
  }

  Widget _feed(
    BuildContext context,
    WidgetRef ref,
    AppLocalizations l10n,
    AsyncValue<List<Channel>> channels,
    AsyncValue<List<JoinedChannel>> joined,
  ) {
    return AdaptiveRefreshableScrollView(
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
        data: (allOwned) {
          final rows = _rows(
            _filterOwned(allOwned),
            _filterJoined(joined.asData?.value ?? const <JoinedChannel>[]),
          );
          return [
            if (rows.isEmpty)
              _emptyState(context, searching: _query.isNotEmpty)
            else
              SliverPadding(
                padding: const EdgeInsets.symmetric(vertical: 4),
                sliver: SliverList.builder(
                  itemCount: rows.length,
                  itemBuilder: (context, i) =>
                      ChannelRow(channel: rows[i].channel, role: rows[i].role),
                ),
              ),
          ];
        },
      ),
    );
  }

  /// The channels to show, newest activity first.
  ///
  /// Owned and joined channels came from two providers and were rendered as two labelled sections,
  /// each in whatever order the server returned. Whether you happen to own a channel is a fact
  /// about permissions, not about what has just happened in it, and it is a poor reason to bury a
  /// channel that posted a minute ago under one that has been silent for a month. They are one list
  /// now, ordered the way the chat list is ordered — which is also what makes the two tabs feel
  /// like the same screen.
  ///
  /// A channel with no posts sorts by nothing and lands at the end; there is no activity to rank it
  /// by, and inventing one would put empty channels above live ones.
  List<_ChannelRowData> _rows(List<Channel> owned, List<JoinedChannel> joined) {
    final rows = [
      for (final c in owned) _ChannelRowData(channel: c),
      for (final j in joined)
        _ChannelRowData(channel: j.channel, role: _roleLabel(j)),
    ];
    rows.sort((a, b) {
      final at = a.channel.lastMessage?.createdAt ?? '';
      final bt = b.channel.lastMessage?.createdAt ?? '';
      if (at.isEmpty && bt.isEmpty) {
        return a.channel.name.toLowerCase().compareTo(
          b.channel.name.toLowerCase(),
        );
      }
      if (at.isEmpty) return 1;
      if (bt.isEmpty) return -1;
      return bt.compareTo(at);
    });
    return rows;
  }

  /// The quiet role suffix on a joined channel's row, or null for a plain subscriber — being one
  /// is the unremarkable case and does not need saying.
  String? _roleLabel(JoinedChannel j) {
    final l10n = context.l10n;
    if (j.memberStatus == MemberStatus.pending) {
      return l10n.t('channel.statusPending');
    }
    if (j.role == ChannelRole.admin) return l10n.t('channel.roleAdmin');
    return null;
  }

  /// Whether the account has any channel at all, before filtering — what decides if there is
  /// anything worth searching.
  bool _hasAnyChannel(
    AsyncValue<List<Channel>> owned,
    AsyncValue<List<JoinedChannel>> joined,
  ) {
    final hasOwned = owned.asData?.value.isNotEmpty ?? false;
    final hasJoined = joined.asData?.value.isNotEmpty ?? false;
    return hasOwned || hasJoined;
  }

  Widget _emptyState(BuildContext context, {required bool searching}) {
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
                searching
                    ? (isCupertino(context)
                          ? CupertinoIcons.search
                          : Icons.search_off)
                    : (isCupertino(context)
                          ? CupertinoIcons.antenna_radiowaves_left_right
                          : Icons.campaign_outlined),
                size: 44,
                color: theme.colorScheme.onSurfaceVariant,
              ),
              const SizedBox(height: 12),
              Text(
                l10n.t(
                  searching ? 'channels.noResults' : 'channels.noChannels',
                ),
                style: theme.textTheme.bodyMedium?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
              // Telling someone to create a channel because they mistyped the name of one they
              // already have is noise, so the hint belongs to the empty case only.
              if (!searching) ...[
                const SizedBox(height: 4),
                Text(
                  l10n.t('channels.noChannelsHint'),
                  textAlign: TextAlign.center,
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

/// A row in the merged list: the channel, and the reader's role in it when that is worth showing.
class _ChannelRowData {
  const _ChannelRowData({required this.channel, this.role});
  final Channel channel;
  final String? role;
}
