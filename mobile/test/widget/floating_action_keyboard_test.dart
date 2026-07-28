// Where the floating action button sits when the keyboard is up.
//
// Android's new-chat pencil and new-channel plus float over the list, and the tab bar floats under
// them. Focusing the search field raises the keyboard; the tab bar rides up with it, because it is
// pinned to the bottom of a scaffold that resizes. The button did not, and sat behind the keyboard
// with no way to reach it.
//
// Geometry rather than a golden, so it runs in CI: goldens are rasterised by the host and excluded
// there, and this is exactly the class of bug that reaches a device between design passes. The
// harness mirrors HomeShell's composition — an outer shell scaffold, the MediaQuery override, an
// inner page scaffold — because the shape is what the bug lives in.

import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/l10n/app_localizations.dart';
import 'package:pheme_mobile/src/widgets/adaptive/adaptive.dart';
import 'package:pheme_mobile/src/widgets/glass/glass.dart';

/// The screen, in logical pixels, as the fake view is configured below.
const _screenHeight = 2000 / 3;

/// What the keyboard covers when it is up, in logical pixels.
const _keyboardHeight = 900 / 3;

Widget _harness() {
  return MaterialApp(
    debugShowCheckedModeBanner: false,
    localizationsDelegates: const [
      AppLocalizations.delegate,
      GlobalMaterialLocalizations.delegate,
      GlobalWidgetsLocalizations.delegate,
      GlobalCupertinoLocalizations.delegate,
    ],
    supportedLocales: AppLocalizations.supportedLocales,
    home: Builder(
      builder: (context) {
        final keyboardUp = MediaQuery.viewInsetsOf(context).bottom > 0;
        return Scaffold(
          body: Builder(
            builder: (context) {
              final media = MediaQuery.of(context);
              return Stack(
                children: [
                  Positioned.fill(
                    child: MediaQuery(
                      data: media.copyWith(
                        padding: media.padding.copyWith(
                          bottom: keyboardUp
                              ? 0
                              : GlassMetrics.tabBarExtent(context),
                        ),
                      ),
                      child: BottomChrome(
                        extent: GlassMetrics.tabBarExtent(context),
                        child: AdaptiveScaffold(
                          behindChrome: true,
                          title: const Text('Pheme'),
                          floatingActionButton: GlassActionButton(
                            icon: Icons.edit_outlined,
                            semanticLabel: 'New chat',
                            onPressed: () {},
                          ),
                          body: ListView(
                            children: [
                              for (var i = 0; i < 30; i++)
                                ListTile(title: Text('Row $i')),
                            ],
                          ),
                        ),
                      ),
                    ),
                  ),
                  Positioned(
                    left: 0,
                    right: 0,
                    bottom: 0,
                    child: GlassTabBar(
                      tabs: const [
                        GlassTab(
                          icon: Icons.chat_bubble_outline,
                          selectedIcon: Icons.chat_bubble,
                          label: 'Chats',
                        ),
                        GlassTab(
                          icon: Icons.campaign_outlined,
                          selectedIcon: Icons.campaign,
                          label: 'Channels',
                        ),
                      ],
                      currentIndex: 0,
                      onSelected: (_) {},
                    ),
                  ),
                ],
              );
            },
          ),
        );
      },
    ),
  );
}

void main() {
  Future<void> pumpAt(WidgetTester tester, {required bool keyboard}) async {
    tester.view.physicalSize = const Size(1170, 2000);
    tester.view.devicePixelRatio = 3;
    // A real platform reports padding as viewPadding MINUS viewInsets: a home indicator underneath
    // a keyboard is not padding anyone can spend. The fake view does not derive that, so it is set
    // by hand — otherwise the assertions below would be off by a home indicator.
    tester.view.padding = keyboard
        ? const FakeViewPadding(top: 141)
        : const FakeViewPadding(top: 141, bottom: 102);
    tester.view.viewInsets = keyboard
        ? const FakeViewPadding(top: 141, bottom: 900)
        : const FakeViewPadding(top: 141);
    addTearDown(tester.view.reset);

    await tester.pumpWidget(_harness());
    await tester.pumpAndSettle();
  }

  testWidgets('with no keyboard, the button floats clear of the tab bar', (
    tester,
  ) async {
    await pumpAt(tester, keyboard: false);

    final button = tester.getRect(find.byType(GlassActionButton));
    final tabBar = tester.getRect(find.byType(GlassTabBar));

    expect(
      button.bottom,
      lessThanOrEqualTo(tabBar.top),
      reason: 'the button must not overlap the bar it floats above',
    );
  });

  testWidgets('with the keyboard up, the button comes up with it', (
    tester,
  ) async {
    await pumpAt(tester, keyboard: true);

    final button = tester.getRect(find.byType(GlassActionButton));
    final keyboardTop = _screenHeight - _keyboardHeight;

    expect(
      button.bottom,
      lessThanOrEqualTo(keyboardTop),
      reason:
          'the button sat behind the keyboard: it is pinned to padding, which the shell '
          'zeroes when the keyboard is up, while the tab bar rides the scaffold resize',
    );
  });

  testWidgets('with the keyboard up, it still clears the tab bar', (
    tester,
  ) async {
    await pumpAt(tester, keyboard: true);

    final button = tester.getRect(find.byType(GlassActionButton));
    final tabBar = tester.getRect(find.byType(GlassTabBar));

    expect(
      button.bottom,
      lessThanOrEqualTo(tabBar.top),
      reason:
          'moving it up must not park it on top of the tab bar, which moved up too',
    );
  });
}
