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
import 'package:pheme_mobile/src/core/snackbar.dart';
import 'package:pheme_mobile/src/theme.dart';
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
    // The app's real theme, not a default one: the chrome's metrics and the snackbar's floating
    // behaviour both come from it, and a harness without it measures a different app.
    theme: lightTheme,
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

  _snackbarTests();

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

// A message reported from a tab must be readable, which means clearing the tab bar.
//
// Same root cause as the button above, and found by looking for it rather than by hitting it: the
// theme floats snackbars 12pt off the bottom of the scaffold, and the tab bar floats INSIDE that
// same scaffold — so the message was drawn underneath the chrome painted over it. Nothing about the
// scaffold's own geometry knows the bar is there; only BottomChrome does.
void _snackbarTests() {
  testWidgets('a snackbar clears the floating tab bar', (tester) async {
    tester.view.physicalSize = const Size(1170, 2000);
    tester.view.devicePixelRatio = 3;
    tester.view.padding = const FakeViewPadding(top: 141, bottom: 102);
    addTearDown(tester.view.reset);

    await tester.pumpWidget(_harness());
    await tester.pumpAndSettle();

    // Reported from inside the page, which is where every real caller reports from — and the only
    // place BottomChrome is visible.
    final context = tester.element(find.byType(ListView));
    notifySuccess(context, 'Saved');
    await tester.pumpAndSettle();

    // The TEXT, not the SnackBar widget: a floating snackbar's own box spans the full width and
    // height it was given and carries the margin inside itself, so measuring it says nothing about
    // where the message actually landed. What is being promised here is that the words are legible,
    // and the words are what to measure.
    final message = tester.getRect(find.text('Saved'));
    final tabBar = tester.getRect(find.byType(GlassTabBar));

    expect(
      message.bottom,
      lessThanOrEqualTo(tabBar.top),
      reason: 'the message was drawn underneath the bar painted over it',
    );
  });
}
