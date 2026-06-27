import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'core/providers.dart';
import 'l10n/app_localizations.dart';
import 'push/push_service.dart';
import 'router.dart';
import 'theme.dart';

/// Root widget: wires theme, locale and the router to the settings state, and
/// routes notification taps to the corresponding message.
class PhemeApp extends ConsumerStatefulWidget {
  const PhemeApp({super.key});

  @override
  ConsumerState<PhemeApp> createState() => _PhemeAppState();
}

class _PhemeAppState extends ConsumerState<PhemeApp> {
  StreamSubscription<MessageRef>? _tapSub;

  @override
  void initState() {
    super.initState();
    final push = ref.read(pushServiceProvider);
    _tapSub = push.onMessageTap.listen(_openMessage);
    // A tap that cold-started the app: navigate once the first frame is ready.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final initial = push.takeInitialMessage();
      if (initial != null) _openMessage(initial);
    });
  }

  @override
  void dispose() {
    _tapSub?.cancel();
    super.dispose();
  }

  void _openMessage(MessageRef target) {
    // Deep-linking requires auth; recipients are normally signed in.
    if (!ref.read(authControllerProvider).isAuthenticated) return;
    ref
        .read(routerProvider)
        .go('/channels/${target.channelId}/messages/${target.messageId}');
  }

  @override
  Widget build(BuildContext context) {
    final router = ref.watch(routerProvider);
    final themeMode = ref.watch(
      settingsControllerProvider.select((s) => s.themeMode),
    );
    final locale = ref.watch(
      settingsControllerProvider.select((s) => s.locale),
    );

    return MaterialApp.router(
      title: 'Pheme',
      debugShowCheckedModeBanner: false,
      theme: lightTheme,
      darkTheme: darkTheme,
      themeMode: themeMode,
      locale: locale,
      supportedLocales: AppLocalizations.supportedLocales,
      localizationsDelegates: const [
        AppLocalizations.delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      routerConfig: router,
    );
  }
}
