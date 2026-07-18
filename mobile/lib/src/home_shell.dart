// The app's two surfaces, side by side.
//
// The web keeps chats and channels in one sidebar, because on a wide screen they can share a column.
// A phone has no such column, and the native answer to "two peer sections" is a tab bar — so that is
// what this is: the same information architecture, expressed the way each platform expresses it.
// Cupertino tabs on iOS and macOS, a Material 3 NavigationBar on Android.
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
    unawaited(ref.read(deviceControllerProvider.notifier).ensureRegistered());
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);

    final pages = const [ConversationsPage(), ChannelsPage()];
    final body = IndexedStack(index: _tab, children: pages);

    if (isCupertino(context)) {
      return CupertinoPageScaffold(
        child: Column(
          children: [
            Expanded(child: body),
            CupertinoTabBar(
              currentIndex: _tab,
              onTap: (i) => setState(() => _tab = i),
              items: [
                BottomNavigationBarItem(
                  icon: const Icon(CupertinoIcons.chat_bubble_2),
                  label: l10n.t('chat.tabChats'),
                ),
                BottomNavigationBarItem(
                  icon: const Icon(
                    CupertinoIcons.antenna_radiowaves_left_right,
                  ),
                  label: l10n.t('chat.tabChannels'),
                ),
              ],
            ),
          ],
        ),
      );
    }

    return Scaffold(
      body: body,
      bottomNavigationBar: NavigationBar(
        selectedIndex: _tab,
        onDestinationSelected: (i) => setState(() => _tab = i),
        destinations: [
          NavigationDestination(
            icon: const Icon(Icons.forum_outlined),
            selectedIcon: const Icon(Icons.forum),
            label: l10n.t('chat.tabChats'),
          ),
          NavigationDestination(
            icon: const Icon(Icons.campaign_outlined),
            selectedIcon: const Icon(Icons.campaign),
            label: l10n.t('chat.tabChannels'),
          ),
        ],
      ),
    );
  }
}
