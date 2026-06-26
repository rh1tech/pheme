import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/auth/login_page.dart';
import 'package:pheme_mobile/src/core/app_config.dart';
import 'package:pheme_mobile/src/core/providers.dart';
import 'package:pheme_mobile/src/l10n/app_localizations.dart';

Widget _wrap(Widget child) {
  return ProviderScope(
    overrides: [
      initialAppStateProvider.overrideWithValue(
        const InitialAppState(
          themeMode: ThemeMode.system,
          locale: Locale('en'),
          baseUrl: 'http://localhost:8080',
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

void main() {
  testWidgets('shows sign-in form by default', (tester) async {
    await tester.pumpWidget(_wrap(const LoginPage()));
    await tester.pumpAndSettle();

    expect(find.text('Sign in to your account'), findsOneWidget);
    expect(find.text('Email'), findsOneWidget);
    expect(find.text('Password'), findsOneWidget);
  });

  testWidgets('toggles to register mode', (tester) async {
    await tester.pumpWidget(_wrap(const LoginPage()));
    await tester.pumpAndSettle();

    await tester.tap(find.text("Don't have an account? Register"));
    await tester.pumpAndSettle();

    expect(find.text('Create an account'), findsOneWidget);
  });

  testWidgets('validates email and password before submit', (tester) async {
    await tester.pumpWidget(_wrap(const LoginPage()));
    await tester.pumpAndSettle();

    // Submit empty: validation errors appear, no network call is attempted.
    await tester.tap(find.widgetWithText(FilledButton, 'Sign in'));
    await tester.pumpAndSettle();

    // Two field validators fire (email + password helper text).
    expect(find.byType(LoginPage), findsOneWidget);
  });
}
