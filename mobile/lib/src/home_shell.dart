// The app's two surfaces, side by side.
//
// The web keeps chats and channels in one sidebar, because on a wide screen they can share a column.
// A phone has no such column, and the native answer to "two peer sections" is a tab bar — so that is
// what this is: the same information architecture, expressed the way each platform expresses it.
// Cupertino tabs on iOS and macOS, a Material 3 NavigationBar on Android.
//
// An IndexedStack rather than a switch, so each tab keeps its scroll position, its loaded pages and
// its open keyboard when the user moves between them.

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import 'channels/channels_page.dart';
import 'chat/conversations_page.dart';
import 'l10n/app_localizations.dart';
import 'widgets/adaptive/platform.dart';

class HomeShell extends StatefulWidget {
  const HomeShell({super.key});

  @override
  State<HomeShell> createState() => _HomeShellState();
}

class _HomeShellState extends State<HomeShell> {
  int _tab = 0;

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
