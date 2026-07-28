// The app's chrome, rendered on its own.
//
// The glass bar, the floating tab bar, the primary action and the composer are now shared by every
// screen on both platforms, so their geometry is worth pinning: a change to a padding token or a
// control size shows up here as a diff rather than as "the iOS build looks off again" three weeks
// later. Both themes, because the glass fill and the hairline are different colours in each and
// only one of them is ever being looked at while the code is written.
//
// Run `flutter test test/golden --update-goldens` after a deliberate change. Tagged `golden` and
// excluded from CI, because the image is rasterised by the host and a blur does not come out byte
// for byte the same on macOS and on Linux — see dart_test.yaml.
@Tags(['golden'])
library;

import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:pheme_mobile/src/l10n/app_localizations.dart';
import 'package:pheme_mobile/src/theme.dart';
import 'package:pheme_mobile/src/widgets/adaptive/adaptive_scaffold.dart';
import 'package:pheme_mobile/src/widgets/adaptive/adaptive_search_field.dart';
import 'package:pheme_mobile/src/widgets/glass/glass.dart';

/// A stand-in for a conversation list: enough rows, in enough colours, that the bar has something
/// to blur and the tab bar has something to float over.
Widget _rows() => ListView.builder(
  padding: EdgeInsets.zero,
  itemCount: 14,
  itemBuilder: (context, i) => Padding(
    padding: const EdgeInsets.symmetric(
      horizontal: GlassMetrics.gutter,
      vertical: 9,
    ),
    child: Row(
      children: [
        Container(
          width: 44,
          height: 44,
          decoration: BoxDecoration(
            color: Colors.primaries[i % Colors.primaries.length],
            shape: BoxShape.circle,
          ),
        ),
        const SizedBox(width: GlassMetrics.gutter),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'Conversation $i',
                style: const TextStyle(fontWeight: FontWeight.w600),
              ),
              const SizedBox(height: 2),
              const Text('The last thing anybody said in it'),
            ],
          ),
        ),
      ],
    ),
  ),
);

Widget _harness({required Brightness brightness}) {
  return MaterialApp(
    debugShowCheckedModeBanner: false,
    theme: brightness == Brightness.dark ? darkTheme : lightTheme,
    // The chrome reads its own strings — the search field's Cancel, a back button's tooltip — so the
    // harness has to carry the app's delegates. Without them this fails at build rather than at
    // comparison, which is how the missing Cancel label showed up here before it reached a device.
    localizationsDelegates: const [
      AppLocalizations.delegate,
      GlobalMaterialLocalizations.delegate,
      GlobalWidgetsLocalizations.delegate,
      GlobalCupertinoLocalizations.delegate,
    ],
    supportedLocales: AppLocalizations.supportedLocales,
    // Mirrors HomeShell EXACTLY — outer scaffold, Builder, MediaQuery override, inner page — rather
    // than approximating it. The shape is the point: a page scaffold nested inside a shell scaffold
    // is what made the keyboard get subtracted twice, and a harness without the outer scaffold
    // cannot show that however carefully the rest is copied.
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
                    // What HomeShell does: the pages are told about the floating tab bar as padding.
                    child: MediaQuery(
                      data: media.copyWith(
                        padding: media.padding.copyWith(
                          bottom: keyboardUp
                              ? 0
                              : GlassMetrics.tabBarExtent(context),
                        ),
                      ),
                      child: AdaptiveScaffold(
                        behindChrome: true,
                        title: const Text('Pheme'),
                        trailing: [
                          GlassIconButton(
                            icon: Icons.settings_outlined,
                            semanticLabel: 'Settings',
                            onPressed: () {},
                          ),
                        ],
                        floatingActionButton: GlassActionButton(
                          icon: Icons.edit_outlined,
                          semanticLabel: 'New chat',
                          onPressed: () {},
                        ),
                        body: Builder(
                          builder: (context) => Column(
                            children: [
                              SizedBox(
                                height: MediaQuery.paddingOf(context).top,
                              ),
                              Padding(
                                padding: const EdgeInsets.fromLTRB(
                                  GlassMetrics.gutter,
                                  GlassMetrics.gap,
                                  GlassMetrics.gutter,
                                  4,
                                ),
                                child: AdaptiveSearchField(
                                  controller: TextEditingController(),
                                  placeholder: 'Search',
                                ),
                              ),
                              Expanded(child: _rows()),
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
                      currentIndex: 0,
                      onSelected: (_) {},
                      tabs: const [
                        GlassTab(
                          icon: Icons.forum_outlined,
                          selectedIcon: Icons.forum,
                          label: 'Chats',
                        ),
                        GlassTab(
                          icon: Icons.campaign_outlined,
                          selectedIcon: Icons.campaign,
                          label: 'Channels',
                        ),
                      ],
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

Widget _composerHarness({required Brightness brightness}) {
  return MaterialApp(
    debugShowCheckedModeBanner: false,
    theme: brightness == Brightness.dark ? darkTheme : lightTheme,
    // The chrome reads its own strings — the search field's Cancel, a back button's tooltip — so the
    // harness has to carry the app's delegates. Without them this fails at build rather than at
    // comparison, which is how the missing Cancel label showed up here before it reached a device.
    localizationsDelegates: const [
      AppLocalizations.delegate,
      GlobalMaterialLocalizations.delegate,
      GlobalWidgetsLocalizations.delegate,
      GlobalCupertinoLocalizations.delegate,
    ],
    supportedLocales: AppLocalizations.supportedLocales,
    home: Scaffold(
      body: Stack(
        children: [
          Positioned.fill(child: _rows()),
          Positioned(
            left: 0,
            right: 0,
            bottom: 0,
            child: GlassComposerBar(
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.end,
                children: [
                  GlassComposerGlyph(
                    icon: Icons.add_photo_alternate_outlined,
                    semanticLabel: 'Attach',
                    onPressed: () {},
                  ),
                  Expanded(
                    child: GlassComposerField(
                      controller: TextEditingController(),
                      hintText: 'Message',
                    ),
                  ),
                  const SizedBox(width: 4),
                  GlassSendButton(
                    sending: false,
                    enabled: true,
                    onPressed: () {},
                    semanticLabel: 'Send',
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    ),
  );
}

/// A dialog with a field in it, which is the only kind that raises a keyboard.
Widget _dialogHarness({required Brightness brightness}) {
  return MaterialApp(
    debugShowCheckedModeBanner: false,
    theme: brightness == Brightness.dark ? darkTheme : lightTheme,
    localizationsDelegates: const [
      AppLocalizations.delegate,
      GlobalMaterialLocalizations.delegate,
      GlobalWidgetsLocalizations.delegate,
      GlobalCupertinoLocalizations.delegate,
    ],
    supportedLocales: AppLocalizations.supportedLocales,
    home: Scaffold(
      body: Stack(
        children: [
          Positioned.fill(child: _rows()),
          GlassDialog(
            title: const Text('Server address'),
            content: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const Text(
                  'Point this app at your own Pheme server. Ask its operator '
                  'for the address, or scan the QR they give you.',
                ),
                const SizedBox(height: 14),
                TextField(controller: TextEditingController()),
              ],
            ),
            actions: [
              GlassDialogAction(label: 'Cancel', onPressed: () {}),
              GlassDialogAction(
                label: 'Save',
                emphasised: true,
                onPressed: () {},
              ),
            ],
          ),
        ],
      ),
    ),
  );
}

void main() {
  for (final brightness in Brightness.values) {
    final name = brightness.name;

    testWidgets('chrome — $name', (tester) async {
      tester.view.physicalSize = const Size(1170, 2000);
      tester.view.devicePixelRatio = 3;
      tester.view.padding = const FakeViewPadding(top: 141, bottom: 102);
      addTearDown(tester.view.reset);

      await tester.pumpWidget(_harness(brightness: brightness));
      await tester.pumpAndSettle();

      await expectLater(
        find.byType(MaterialApp),
        matchesGoldenFile('goldens/chrome_$name.png'),
      );
    });

    // A dialog with the keyboard up.
    //
    // The third inset bug of the same afternoon, and the same shape as the other two: a surface that
    // centres against the whole screen while the keyboard owns the bottom third of it, so the part
    // that gets hidden is the part with the buttons. What this pins is that the actions are on
    // screen — a dialog you cannot dismiss or confirm is worse than one that looks cramped.
    testWidgets('dialog with keyboard — $name', (tester) async {
      tester.view.physicalSize = const Size(1170, 2000);
      tester.view.devicePixelRatio = 3;
      tester.view.padding = const FakeViewPadding(top: 141);
      tester.view.viewInsets = const FakeViewPadding(top: 141, bottom: 900);
      addTearDown(tester.view.reset);

      await tester.pumpWidget(_dialogHarness(brightness: brightness));
      await tester.pumpAndSettle();

      await expectLater(
        find.byType(MaterialApp),
        matchesGoldenFile('goldens/dialog_keyboard_$name.png'),
      );
    });

    // With a keyboard up. The case that has gone wrong twice: the chat list ended up 208pt tall on a
    // 874pt screen because a 333pt keyboard was subtracted by the shell AND again by the page. What
    // this golden pins is that the list still reaches the tab bar, and the tab bar still sits on the
    // keyboard rather than being dragged into the middle of the list.
    testWidgets('chrome with keyboard — $name', (tester) async {
      tester.view.physicalSize = const Size(1170, 2000);
      tester.view.devicePixelRatio = 3;
      // bottom: 0, because a real platform reports padding as viewPadding MINUS viewInsets — a home
      // indicator under a keyboard is not padding anyone can use. The fake view does not derive it,
      // so it is set by hand; leaving it at 102 would put the tab bar a home indicator's height off
      // where a device puts it, and the golden would pin the wrong picture.
      tester.view.padding = const FakeViewPadding(top: 141);
      tester.view.viewInsets = const FakeViewPadding(top: 141, bottom: 900);
      addTearDown(tester.view.reset);

      await tester.pumpWidget(_harness(brightness: brightness));
      await tester.pumpAndSettle();

      await expectLater(
        find.byType(MaterialApp),
        matchesGoldenFile('goldens/chrome_keyboard_$name.png'),
      );
    });

    testWidgets('composer — $name', (tester) async {
      tester.view.physicalSize = const Size(1170, 900);
      tester.view.devicePixelRatio = 3;
      tester.view.padding = const FakeViewPadding(bottom: 102);
      addTearDown(tester.view.reset);

      await tester.pumpWidget(_composerHarness(brightness: brightness));
      await tester.pumpAndSettle();

      await expectLater(
        find.byType(MaterialApp),
        matchesGoldenFile('goldens/composer_$name.png'),
      );
    });
  }
}
