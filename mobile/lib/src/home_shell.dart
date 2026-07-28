// The app's two surfaces, side by side.
//
// The web keeps chats and channels in one sidebar, because on a wide screen they can share a column.
// A phone has no such column, and the answer to "two peer sections" is a tab bar — one floating
// glass bar, the same on both platforms, rather than a CupertinoTabBar on one and a Material
// NavigationBar on the other. See GlassTabBar for why it floats.
//
// An IndexedStack rather than a switch, so each tab keeps its scroll position, its loaded pages and
// its open keyboard when the user moves between them.

import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'channels/channels_page.dart';
import 'core/providers.dart';
import 'chat/conversations_page.dart';
import 'l10n/app_localizations.dart';
import 'widgets/adaptive/platform.dart';
import 'widgets/glass/glass.dart';

class HomeShell extends ConsumerStatefulWidget {
  const HomeShell({super.key});

  @override
  ConsumerState<HomeShell> createState() => _HomeShellState();
}

class _HomeShellState extends ConsumerState<HomeShell> {
  int _tab = 0;

  @override
  void initState() {
    super.initState();
    // Register this device the moment the user reaches the app, rather than when they happen to
    // press a bell on the Channels tab.
    //
    // That bell was the ONLY path to it, and a device with no registration gets no push at all —
    // so anyone who only used Chats never had notifications and the settings screen told them, in
    // as many words, that the device was not registered. There was nothing they could do about it
    // from any surface they visited.
    //
    // Best-effort and silent: ensureRegistered swallows failures, and a device still registers
    // WITHOUT a push token if permission is refused — which matters, because the id is also what
    // the call answer-lock is keyed on. Nothing here shows a toast; this is not an action the user
    // asked for and should not report back to them.
    final devices = ref.read(deviceControllerProvider.notifier);
    unawaited(
      devices.ensureRegistered().then(
        // Having an id is not the same as the server having a working address for it. A token that
        // rotated while the app was closed — a reinstall, cleared data — raises no event the app can
        // hear, so a device that is registered can still be unreachable, and nothing says so.
        (_) => devices.refreshRegistrationIfTokenChanged(),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final ios = isCupertino(context);
    final media = MediaQuery.of(context);

    // Each platform keeps its own icon set — a chat bubble is drawn differently by SF Symbols and by
    // Material, and borrowing one platform's glyphs for the other is the one place where "identical"
    // reads as wrong rather than as consistent. The metrics around them are shared.
    final tabs = [
      GlassTab(
        icon: ios ? CupertinoIcons.chat_bubble_2 : Icons.forum_outlined,
        selectedIcon: ios ? CupertinoIcons.chat_bubble_2_fill : Icons.forum,
        label: l10n.t('chat.tabChats'),
      ),
      GlassTab(
        icon: ios
            ? CupertinoIcons.antenna_radiowaves_left_right
            : Icons.campaign_outlined,
        selectedIcon: ios
            ? CupertinoIcons.antenna_radiowaves_left_right
            : Icons.campaign,
        label: l10n.t('chat.tabChannels'),
      ),
    ];

    // Whether a keyboard is covering the bottom of the screen. Read HERE, above the scaffold, since
    // the scaffold consumes the inset and the view below it can no longer tell.
    final keyboardUp = media.viewInsets.bottom > 0;

    return Scaffold(
      backgroundColor: Theme.of(context).colorScheme.surface,
      // Everything below reads the scaffold's OWN MediaQuery, not the one above it.
      //
      // A scaffold that resizes for the keyboard also strips the inset it just consumed, so that
      // nothing inside subtracts it a second time. Passing the outer MediaQuery down put the inset
      // back — and the page's own scaffold, seeing a keyboard that had already been made room for,
      // shrank by its full height again. Two subtractions of a 333pt keyboard from an 874pt screen
      // left the chat list 208pt tall, which is exactly where it was being cut off.
      body: Builder(
        builder: (context) {
          final media = MediaQuery.of(context);
          return Stack(
            children: [
              Positioned.fill(
                // The tab bar floats over the pages, so the pages are told about it the same way they
                // are told about the home indicator: as bottom padding. Their lists spend it as content
                // padding, and the last chat in the list comes to rest clear of the bar instead of
                // under it.
                //
                // Not while the keyboard is up, though. There the screen is already short, and spending
                // a bar's height of it on blank space is what made a focused search look like it had
                // cut the list off. The bar stays visible and the rows run beneath it — which is what
                // the glass is FOR: content passing under it reads as continuing, where content
                // stopping short of it reads as the end of the list.
                child: MediaQuery(
                  data: media.copyWith(
                    padding: media.padding.copyWith(
                      bottom: keyboardUp
                          ? 0
                          : GlassMetrics.tabBarExtent(context),
                    ),
                  ),
                  child: IndexedStack(
                    index: _tab,
                    children: const [ConversationsPage(), ChannelsPage()],
                  ),
                ),
              ),
              Positioned(
                left: 0,
                right: 0,
                bottom: 0,
                child: GlassTabBar(
                  tabs: tabs,
                  currentIndex: _tab,
                  onSelected: (i) => setState(() => _tab = i),
                ),
              ),
            ],
          );
        },
      ),
    );
  }
}
