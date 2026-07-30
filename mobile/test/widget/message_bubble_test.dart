// The bubble carries the whole own/other distinction in its geometry — no tail, no avatar, just which
// bottom corner is squared off. Grouping then reuses that same corner to say "this run is one
// utterance". So the corners are load-bearing, and a swap is exactly the kind of thing that looks
// almost right and reads wrong.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:intl/date_symbol_data_local.dart';
import 'package:pheme_mobile/src/l10n/app_localizations.dart';
import 'package:pheme_mobile/src/chat/widgets/message_bubble.dart';

Future<BorderRadius> _cornersOf(
  WidgetTester tester, {
  required bool isOwn,
  required bool endsRun,
}) async {
  await tester.pumpWidget(
    MaterialApp(
      localizationsDelegates: const [AppLocalizations.delegate],
      supportedLocales: AppLocalizations.supportedLocales,
      home: Scaffold(
        body: MessageBubble(
          body: 'hello',
          createdAt: '2026-07-14T10:00:00Z',
          isOwn: isOwn,
          endsRun: endsRun,
        ),
      ),
    ),
  );
  await tester.pump();

  final container = tester.widget<Container>(
    find
        .descendant(
          of: find.byType(MessageBubble),
          matching: find.byType(Container),
        )
        .first,
  );
  final decoration = container.decoration! as BoxDecoration;
  return decoration.borderRadius! as BorderRadius;
}

void main() {
  // The app does this in main(). Without it every DateFormat built with an explicit locale throws.
  setUpAll(initializeDateFormatting);

  const round = Radius.circular(16);
  const tail = Radius.circular(2);

  group('bubble geometry', () {
    testWidgets('my message squares off the BOTTOM-RIGHT corner', (
      tester,
    ) async {
      final corners = await _cornersOf(tester, isOwn: true, endsRun: true);
      expect(corners.bottomRight, tail);
      expect(corners.bottomLeft, round);
    });

    testWidgets('their message squares off the BOTTOM-LEFT corner', (
      tester,
    ) async {
      final corners = await _cornersOf(tester, isOwn: false, endsRun: true);
      expect(corners.bottomLeft, tail);
      expect(corners.bottomRight, round);
    });

    // The tail belongs to the END of a run, not to every message in it. Without this, a run of five
    // messages is five identical blocks with five tails — which is exactly the stacked, shouty look
    // that grouping exists to remove.
    testWidgets('a message mid-run has no tail at all', (tester) async {
      final mine = await _cornersOf(tester, isOwn: true, endsRun: false);
      expect(mine.bottomRight, round);
      expect(mine.bottomLeft, round);

      final theirs = await _cornersOf(tester, isOwn: false, endsRun: false);
      expect(theirs.bottomLeft, round);
      expect(theirs.bottomRight, round);
    });
  });

  group('what a run shows', () {
    testWidgets('the sender name appears only at the START of a run', (
      tester,
    ) async {
      Future<void> pump({required bool startsRun}) => tester.pumpWidget(
        MaterialApp(
          localizationsDelegates: const [AppLocalizations.delegate],
          supportedLocales: AppLocalizations.supportedLocales,
          home: Scaffold(
            body: MessageBubble(
              body: 'hello',
              createdAt: '2026-07-14T10:00:00Z',
              isOwn: false,
              senderName: 'Ada',
              startsRun: startsRun,
            ),
          ),
        ),
      );

      // The extra pump matters: MaterialApp resolves its Localizations on the frame AFTER pumpWidget,
      // and the bubble reads them.
      await pump(startsRun: true);
      await tester.pump();
      expect(find.text('Ada'), findsOneWidget);

      await pump(startsRun: false);
      await tester.pump();
      expect(find.text('Ada'), findsNothing);
    });

    testWidgets('the timestamp appears only at the END of a run', (
      tester,
    ) async {
      Future<void> pump({required bool endsRun}) => tester.pumpWidget(
        MaterialApp(
          localizationsDelegates: const [AppLocalizations.delegate],
          supportedLocales: AppLocalizations.supportedLocales,
          home: Scaffold(
            body: MessageBubble(
              body: 'hello',
              createdAt: '2026-07-14T10:00:00Z',
              isOwn: true,
              endsRun: endsRun,
            ),
          ),
        ),
      );

      // Five messages sent in the same minute do not need five identical clocks down their side.
      await pump(endsRun: true);
      await tester.pump();
      expect(find.textContaining(':'), findsOneWidget);

      await pump(endsRun: false);
      await tester.pump();
      expect(find.textContaining(':'), findsNothing);
    });
  });

  // A message this device cannot read says so, permanently, and must never look like a spinner: MLS
  // gives a device no access to what was said before it joined.
  testWidgets('an unreadable message says so rather than showing nothing', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        localizationsDelegates: const [AppLocalizations.delegate],
        supportedLocales: AppLocalizations.supportedLocales,
        home: const Scaffold(
          body: MessageBubble(
            body: null,
            createdAt: '2026-07-14T10:00:00Z',
            isOwn: false,
          ),
        ),
      ),
    );
    await tester.pump();

    expect(find.text('Not available on this device'), findsOneWidget);
    expect(find.byType(CircularProgressIndicator), findsNothing);
  });

  // The signature and the envelope name different people. The bubble must not quietly pick one:
  // that is the misattribution the authenticated sender exists to prevent, and it is exactly the
  // case a reader has no other way to notice.
  group('an unverified sender', () {
    Future<void> pump(
      WidgetTester tester, {
      required bool unverified,
      String? senderName,
    }) async {
      await tester.pumpWidget(
        MaterialApp(
          localizationsDelegates: const [AppLocalizations.delegate],
          supportedLocales: AppLocalizations.supportedLocales,
          home: Scaffold(
            body: MessageBubble(
              body: 'hello',
              createdAt: '2026-07-14T10:00:00Z',
              isOwn: false,
              senderName: senderName,
              senderUnverified: unverified,
            ),
          ),
        ),
      );
      await tester.pump();
    }

    testWidgets('is said out loud, not silently resolved', (tester) async {
      await pump(tester, unverified: true);
      expect(
        find.textContaining('Unverified sender'),
        findsOneWidget,
        reason:
            'a message whose signature contradicts its envelope must say so — rendering it '
            'under either name is the attack succeeding',
      );
      // The message itself is real: MLS decrypted it. Only the attribution is in doubt.
      expect(find.text('hello'), findsOneWidget);
    });

    testWidgets('says nothing when the two agree', (tester) async {
      await pump(tester, unverified: false, senderName: 'Alice');
      expect(find.textContaining('Unverified sender'), findsNothing);
      expect(find.text('Alice'), findsOneWidget);
    });
  });
}
