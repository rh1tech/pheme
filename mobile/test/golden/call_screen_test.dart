// The dialer, rendered without a call.
//
// A call is the hardest screen in the app to look at: placing a real one needs two devices, a TURN
// server and somebody on the other end, so in practice its layout gets checked by whoever happens to
// ring somebody. These pin the three states that matter — placing, connected, and being rung — so a
// change to them is visible before it ships rather than during somebody's phone call.
//
// Tagged `golden` and excluded from CI; see dart_test.yaml.
@Tags(['golden'])
library;

import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:pheme_mobile/src/calls/call_controller.dart';
import 'package:pheme_mobile/src/calls/call_screen.dart';
import 'package:pheme_mobile/src/calls/call_state.dart';
import 'package:pheme_mobile/src/l10n/app_localizations.dart';
import 'package:pheme_mobile/src/theme.dart';

/// A controller that holds a state and drives nothing.
///
/// The real one opens the live-event stream from `build()`, which a widget test has no business
/// standing up. Its action methods all null-guard on the engine, so they are safe to leave inherited
/// — a tap in a golden does nothing, which is what a golden wants.
class _StaticCallController extends CallController {
  _StaticCallController(this._state);

  final CallState _state;

  @override
  CallState? build() => _state;
}

CallState _call({
  required CallStatus status,
  bool outgoing = true,
  bool muted = false,
  AudioRoute route = AudioRoute.earpiece,
  int seconds = 0,
}) => CallState(
  callId: 'call-1',
  conversationId: 'conv-1',
  status: status,
  outgoing: outgoing,
  muted: muted,
  route: route,
  seconds: seconds,
  inviteReady: true,
);

Widget _harness({required CallState call, required Widget child}) {
  return ProviderScope(
    overrides: [
      callProvider.overrideWith(() => _StaticCallController(call)),
      // The party is resolved from the conversation store in the app; here it is simply given, so
      // the golden shows a name and an avatar rather than the blank a missing conversation leaves.
      callPartyProvider(
        'conv-1',
      ).overrideWithValue(const CallParty(id: 'user-2', name: 'Juliett Smile')),
    ],
    child: MaterialApp(
      debugShowCheckedModeBanner: false,
      theme: lightTheme,
      localizationsDelegates: const [
        AppLocalizations.delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      supportedLocales: AppLocalizations.supportedLocales,
      home: child,
    ),
  );
}

void main() {
  setUp(() {
    // The app's theme is light here on purpose: the dialer must come out dark anyway. If a change
    // ever makes it follow the colour scheme, these goldens go pale and say so.
  });

  Future<void> pumpAt(WidgetTester tester, Widget app) async {
    tester.view.physicalSize = const Size(1170, 2532);
    tester.view.devicePixelRatio = 3;
    tester.view.padding = const FakeViewPadding(top: 141, bottom: 102);
    addTearDown(tester.view.reset);
    await tester.pumpWidget(app);
    await tester.pumpAndSettle();
  }

  testWidgets('placing a call', (tester) async {
    final call = _call(status: CallStatus.calling);
    await pumpAt(
      tester,
      _harness(
        call: call,
        child: CallScreen(call: call, onMinimise: () {}),
      ),
    );
    await expectLater(
      find.byType(MaterialApp),
      matchesGoldenFile('goldens/call_placing.png'),
    );
  });

  testWidgets('connected, muted, on speaker', (tester) async {
    final call = _call(
      status: CallStatus.connected,
      muted: true,
      route: AudioRoute.speaker,
      seconds: 754,
    );
    await pumpAt(
      tester,
      _harness(
        call: call,
        child: CallScreen(call: call, onMinimise: () {}),
      ),
    );
    await expectLater(
      find.byType(MaterialApp),
      matchesGoldenFile('goldens/call_connected.png'),
    );
  });

  testWidgets('being rung', (tester) async {
    final call = _call(status: CallStatus.ringing, outgoing: false);
    await pumpAt(
      tester,
      _harness(
        call: call,
        child: IncomingCallScreen(call: call),
      ),
    );
    await expectLater(
      find.byType(MaterialApp),
      matchesGoldenFile('goldens/call_incoming.png'),
    );
  });
}
