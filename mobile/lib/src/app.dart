import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'chat/chat_providers.dart';
import 'core/deep_links.dart';
import 'core/providers.dart';
import 'l10n/app_localizations.dart';
import 'push/push_service.dart';
import 'calls/call_ui.dart';
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
  StreamSubscription<NotificationTarget>? _tapSub;

  @override
  void initState() {
    super.initState();
    final push = ref.read(pushServiceProvider);
    _tapSub = push.onMessageTap.listen(_openMessage);
    // Start listening for pheme:// links. The controller PARKS whatever arrives rather than acting
    // on it, because the screen that can act — the login form for an invitation, the channel list
    // for a join — may not exist yet on a cold start. Each takes its own when it is ready.
    unawaited(ref.read(deepLinkControllerProvider.notifier).start());
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

  /// Acts on a link that needs no particular screen: pointing this install at another server.
  ///
  /// The other two kinds are claimed by the screens that can use them, so they are not handled
  /// here — see LoginPage (invitations) and ChannelsPage (joins).
  Future<void> _applyServerLink(ServerLink link) async {
    await ref.read(settingsControllerProvider.notifier).setBaseUrl(link.server);
  }

  void _openMessage(NotificationTarget target) {
    // Deep-linking requires auth; recipients are normally signed in.
    if (!ref.read(authControllerProvider).isAuthenticated) return;
    final router = ref.read(routerProvider);
    switch (target) {
      case ChannelMessageTarget(:final channelId, :final messageId):
        router.go('/channels/$channelId/messages/$messageId');
      // Chats had no case at all: the tap plumbing was written for channel broadcasts, so a
      // notification about a message from a person resolved to nothing and the app simply opened
      // wherever it had been left. That is indistinguishable from the tap being ignored.
      case ConversationTarget(:final conversationId):
        router.go('/chats/$conversationId');
    }
  }

  @override
  Widget build(BuildContext context) {
    final router = ref.watch(routerProvider);
    // Keep the push service's "which chat is open" in step with the app, so it can suppress a
    // notification for a conversation already on screen. A plain field on the service, updated here
    // where a Ref is available, rather than the service reaching into Riverpod itself.
    ref.listen(activeConversationIdProvider, (_, next) {
      ref.read(pushServiceProvider).activeConversationId = next;
    });
    // A server link is the one kind no screen is waiting for, so it is taken here.
    ref.listen(deepLinkControllerProvider, (_, next) {
      if (next is! ServerLink) return;
      final link = ref
          .read(deepLinkControllerProvider.notifier)
          .consume<ServerLink>();
      if (link != null) unawaited(_applyServerLink(link));
    });
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
      // Cupertino widgets used on iOS read their accent/brightness from the
      // ambient CupertinoTheme rather than Material's ThemeData, so seed one
      // with the Iris brand colour and the resolved brightness.
      builder: (context, child) => CupertinoTheme(
        data: _cupertinoTheme(context, themeMode),
        // The call surface sits ABOVE the router, not inside a route. A call outlives the screen it
        // was placed from — walk back to the conversation list mid-call and the bar comes with you —
        // and an incoming call has to be able to ring over whatever happens to be on screen.
        child: CallOverlay(child: child ?? const SizedBox.shrink()),
      ),
    );
  }

  /// Brand-tinted Cupertino theme whose brightness follows the app theme mode
  /// (falling back to the platform brightness for [ThemeMode.system]).
  CupertinoThemeData _cupertinoTheme(BuildContext context, ThemeMode mode) {
    final brightness = switch (mode) {
      ThemeMode.light => Brightness.light,
      ThemeMode.dark => Brightness.dark,
      ThemeMode.system => MediaQuery.platformBrightnessOf(context),
    };
    return CupertinoThemeData(
      brightness: brightness,
      primaryColor: kIris,
      applyThemeToAll: true,
    );
  }
}
