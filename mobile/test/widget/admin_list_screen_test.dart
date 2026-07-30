import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/admin/admin_models.dart';
import 'package:pheme_mobile/src/admin/widgets/admin_ui.dart';
import 'package:pheme_mobile/src/core/app_config.dart';
import 'package:pheme_mobile/src/core/providers.dart';
import 'package:pheme_mobile/src/l10n/app_localizations.dart';

/// Every admin listing is an instance of AdminListScreen, so its state machine — first load,
/// search, paging, empty — is worth pinning once here rather than five times over.

Widget _wrap(Widget child) {
  return ProviderScope(
    overrides: [
      initialAppStateProvider.overrideWithValue(
        const InitialAppState(
          themeMode: ThemeMode.system,
          locale: Locale('en'),
          baseUrl: 'http://localhost:8080',
          savedBaseUrl: 'http://localhost:8080',
          deviceId: null,
        ),
      ),
    ],
    child: MaterialApp(
      locale: const Locale('en'),
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

/// A fake listing of `total` rows named "row N", served a page at a time and filtered by substring
/// — enough to behave like the server without one.
AdminPage<String> _fakePage(String query, int page, {int total = 45}) {
  final all = [
    for (var i = 1; i <= total; i++) 'row $i',
  ].where((r) => query.isEmpty || r.contains(query)).toList();
  final start = (page - 1) * adminPageLimit;
  final end = (start + adminPageLimit).clamp(0, all.length);
  return AdminPage<String>(
    items: start >= all.length ? const [] : all.sublist(start, end),
    total: all.length,
    page: page,
    limit: adminPageLimit,
  );
}

void main() {
  // A tall surface, so a full page of rows AND the pager under them are on screen at once. The
  // alternative is scrolling to the pager in every test, and a scroll that lands under the floating
  // chrome fails to hit-test in a way that says nothing about the code under test.
  setUp(() {
    final view = TestWidgetsFlutterBinding.ensureInitialized()
        .platformDispatcher
        .views
        .first;
    view.physicalSize = const Size(1200, 4800);
    view.devicePixelRatio = 1.0;
  });

  tearDown(() {
    final view = TestWidgetsFlutterBinding.ensureInitialized()
        .platformDispatcher
        .views
        .first;
    view.resetPhysicalSize();
    view.resetDevicePixelRatio();
  });

  testWidgets('shows the first page and its pager', (tester) async {
    await tester.pumpWidget(
      _wrap(
        AdminListScreen<String>(
          title: 'Rows',
          emptyLabel: 'Nothing here.',
          fetch: (q, p) async => _fakePage(q, p),
          rowBuilder: (context, item, reload) => ListTile(title: Text(item)),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('row 1'), findsOneWidget);
    // 45 rows at 20 a page: three pages.
    expect(find.text('Page 1 of 3'), findsOneWidget);
  });

  testWidgets('paging forward fetches the next page', (tester) async {
    await tester.pumpWidget(
      _wrap(
        AdminListScreen<String>(
          title: 'Rows',
          emptyLabel: 'Nothing here.',
          fetch: (q, p) async => _fakePage(q, p),
          rowBuilder: (context, item, reload) => ListTile(title: Text(item)),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.text('Next'));
    await tester.pumpAndSettle();

    expect(find.text('Page 2 of 3'), findsOneWidget);
    expect(find.text('row 21'), findsOneWidget);
  });

  // The bug this guards: searching from page 3 kept the page number, so a search matching two rows
  // came back empty and read as "no matches" when there were plenty.
  testWidgets('a new search returns to page 1', (tester) async {
    var lastPageAsked = -1;
    await tester.pumpWidget(
      _wrap(
        AdminListScreen<String>(
          title: 'Rows',
          searchPlaceholder: 'Search',
          emptyLabel: 'Nothing here.',
          fetch: (q, p) async {
            lastPageAsked = p;
            return _fakePage(q, p);
          },
          rowBuilder: (context, item, reload) => ListTile(title: Text(item)),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.text('Next'));
    await tester.pumpAndSettle();
    expect(lastPageAsked, 2);

    await tester.enterText(find.byType(TextField).first, 'row 4');
    // Past the search debounce.
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(lastPageAsked, 1);
    // Scoped to the rows: the search field itself now reads "row 4" too.
    expect(find.widgetWithText(ListTile, 'row 4'), findsOneWidget);
    expect(find.widgetWithText(ListTile, 'row 40'), findsOneWidget);
  });

  testWidgets('says so when a search matches nothing', (tester) async {
    await tester.pumpWidget(
      _wrap(
        AdminListScreen<String>(
          title: 'Rows',
          searchPlaceholder: 'Search',
          emptyLabel: 'Nothing here.',
          fetch: (q, p) async => _fakePage(q, p),
          rowBuilder: (context, item, reload) => ListTile(title: Text(item)),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.enterText(find.byType(TextField).first, 'nonexistent');
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pumpAndSettle();

    expect(find.text('Nothing here.'), findsOneWidget);
    // No pager on an empty result: there is nothing to page through.
    expect(find.text('Next'), findsNothing);
  });

  testWidgets('a failed first load offers a retry', (tester) async {
    var attempts = 0;
    await tester.pumpWidget(
      _wrap(
        AdminListScreen<String>(
          title: 'Rows',
          emptyLabel: 'Nothing here.',
          fetch: (q, p) async {
            attempts++;
            if (attempts == 1) throw Exception('boom');
            return _fakePage(q, p);
          },
          rowBuilder: (context, item, reload) => ListTile(title: Text(item)),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Could not load'), findsOneWidget);

    await tester.tap(find.text('Retry'));
    await tester.pumpAndSettle();

    expect(find.text('row 1'), findsOneWidget);
  });

  testWidgets('a one-page listing has no pager', (tester) async {
    await tester.pumpWidget(
      _wrap(
        AdminListScreen<String>(
          title: 'Rows',
          emptyLabel: 'Nothing here.',
          fetch: (q, p) async => _fakePage(q, p, total: 5),
          rowBuilder: (context, item, reload) => ListTile(title: Text(item)),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('row 5'), findsOneWidget);
    expect(find.text('Next'), findsNothing);
    expect(find.text('Previous'), findsNothing);
  });
}
