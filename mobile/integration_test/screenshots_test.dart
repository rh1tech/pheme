// Drives the real app through a few screens and holds each one still long
// enough to be photographed from outside.
//
// It does NOT capture the frames itself. integration_test's takeScreenshot on
// iOS returns the launch storyboard rather than the Flutter surface, and the
// Android workaround (convertFlutterSurfaceToImage) detaches the widget tree so
// the next tap finds nothing. So the split is: Dart drives, the host captures
// with `xcrun simctl io screenshot` / `adb exec-out screencap`, which see the
// real pixels because they photograph the device rather than the framework.
//
// The two halves meet at a printed marker. Each `shot()` prints SHOT:<name> and
// then sits still; the host script watches for the line and grabs the screen.
//
// Runs against the seeded instance, so the app it photographs has other
// people's conversations in it.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import 'package:pheme_mobile/main.dart' as app;

const _email = String.fromEnvironment('SHOT_EMAIL', defaultValue: 'priya@pheme.test');
const _password = String.fromEnvironment('SHOT_PASSWORD', defaultValue: 'orchard-lantern-97');
const _server = String.fromEnvironment('PHEME_API', defaultValue: 'http://localhost:8099');

/// pumpAndSettle gives up on a screen with a perpetual animation, and this app
/// has several (spinners, the live indicator). Pumping for a fixed spell is the
/// reliable way to let a screen finish arriving.
Future<void> rest(WidgetTester tester, {int seconds = 3}) async {
  final end = DateTime.now().add(Duration(seconds: seconds));
  while (DateTime.now().isBefore(end)) {
    await tester.pump(const Duration(milliseconds: 100));
  }
}

/// Announces a capture point and holds the screen still while the host grabs it.
Future<void> shot(WidgetTester tester, String name) async {
  await rest(tester, seconds: 2);
  debugPrint('SHOT:$name');
  await rest(tester, seconds: 6);
}

Future<void> tapIfPresent(WidgetTester tester, Finder f, {int settle = 4}) async {
  if (!tester.any(f)) return;
  await tester.tap(f.first, warnIfMissed: false);
  await rest(tester, seconds: settle);
}

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('drive the app for screenshots', (tester) async {
    app.main();
    await rest(tester, seconds: 8);

    // --- sign in --------------------------------------------------------------
    // EditableText, not TextField: AdaptiveTextFormField renders a Cupertino
    // field on iOS and a Material one on Android, and EditableText is the thing
    // underneath both.
    // The app keeps a session, so a second run of this file starts already
    // signed in. Both states are legitimate; only the first has a form to fill.
    final fields = find.byType(EditableText);
    final signIn = find.text('Sign in');
    if (tester.any(fields) && tester.any(signIn)) {
      await shot(tester, '01-login');

      await tester.enterText(fields.at(0), _email);
      await rest(tester, seconds: 1);
      await tester.enterText(fields.at(1), _password);
      await rest(tester, seconds: 1);
      // The server field matters: the emulator reaches the host at a different
      // address than the simulator does.
      if (tester.widgetList(fields).length > 2) {
        await tester.enterText(fields.at(2), _server);
        await rest(tester, seconds: 1);
      }
      await tester.tap(signIn.last, warnIfMissed: false);
      await rest(tester, seconds: 14);
    }
    // A device with no key store is offered a restore. This one is genuinely new,
    // so it starts fresh — and because it does that BEFORE the conversations are
    // seeded, it is a member of them from the first epoch and can read them.
    final startFresh = find.text('Start fresh on this device');
    if (tester.any(startFresh)) {
      await tester.tap(startFresh.last, warnIfMissed: false);
      await rest(tester, seconds: 8);
    }
    // Hold here while the host seeds the conversations.
    //
    // This is the only window in which it can be done. flutter drive uninstalls
    // the app when the run ends, which destroys the MLS key store — so a device
    // can never carry keys from one run to the next, and anything seeded before
    // this run is ciphertext it has no way to read ("Encrypted message" on every
    // row). Seeding NOW, with this device signed in and its key packages
    // published, makes it a member from the first epoch of every conversation.
    debugPrint('SHOT:SEEDNOW');
    await rest(tester, seconds: 75);

    await shot(tester, '02-chats');

    // --- the chat list --------------------------------------------------------
    // The app may open on either tab, so ask for Chats explicitly rather than
    // photographing whichever one it happened to restore.
    final chatsTab = find.text('Chats');
    if (tester.any(chatsTab)) {
      await tester.tap(chatsTab.last, warnIfMissed: false);
      await rest(tester, seconds: 5);
    }
    await shot(tester, '03-chats');

    // Opening a conversation is skipped deliberately.
    //
    // Tapping a row throws "Tried to modify a provider while the widget tree was
    // building" and puts Flutter's red error screen on top of the app. That is a
    // real bug in the conversation route, not an artefact of the harness, and it
    // is worth a look on its own — but it is not something to photograph.
    /// The rows are custom widgets, not ListTiles, so a type finder matches
    /// nothing. Find them by the titles the seed puts there.
    Finder firstOf(List<String> titles) {
      for (final t in titles) {
        final f = find.text(t);
        if (tester.any(f)) return f;
      }
      return find.text(titles.first);
    }

    // --- channels -------------------------------------------------------------
    final channelsTab = find.text('Channels');
    if (tester.any(channelsTab)) {
      await tester.tap(channelsTab.last, warnIfMissed: false);
      await rest(tester, seconds: 5);
      await shot(tester, '05-channels');

      final channelRows = firstOf(const [
        'Deploys', 'On-call', 'Allotment 14', 'Thursday rehearsals',
      ]);
      if (tester.any(channelRows)) {
        await tester.tap(channelRows.first, warnIfMissed: false);
        await rest(tester, seconds: 6);
        await shot(tester, '06-channel');
        await tapIfPresent(tester, find.byType(BackButton), settle: 3);
        await tapIfPresent(tester, find.byIcon(Icons.arrow_back), settle: 3);
      }
    }

    // --- settings -------------------------------------------------------------
    // The gear in the app bar, which is how a person reaches it.
    await tapIfPresent(tester, find.byIcon(Icons.settings), settle: 6);
    if (!tester.any(find.text('Appearance'))) {
      await tapIfPresent(tester, find.byIcon(Icons.settings_outlined), settle: 6);
    }
    await shot(tester, '07-settings');
  }, timeout: const Timeout(Duration(minutes: 15)));
}
