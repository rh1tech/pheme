import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import 'auth/forgot_password_page.dart';
import 'auth/login_page.dart';
import 'channels/channel_page.dart';
import 'channels/message_page.dart';
import 'chat/conversation_chat_page.dart';
import 'core/providers.dart';
import 'crypto/recovery_gate.dart';
import 'home_shell.dart';
import 'profile/profile_page.dart';
import 'settings/security_page.dart';
import 'settings/settings_page.dart';

/// The app router. Redirects to /login when unauthenticated and away from
/// /login once signed in; refreshes whenever the auth state changes.
final routerProvider = Provider<GoRouter>((ref) {
  final refresh = ValueNotifier<int>(0);
  ref.listen(authControllerProvider, (_, _) => refresh.value++);
  ref.onDispose(refresh.dispose);

  return GoRouter(
    initialLocation: '/',
    refreshListenable: refresh,
    redirect: (context, state) {
      final authed = ref.read(authControllerProvider).isAuthenticated;
      final loc = state.matchedLocation;
      final onAuthPage = loc == '/login' || loc == '/forgot-password';
      if (!authed) return onAuthPage ? null : '/login';
      if (onAuthPage) return '/';
      return null;
    },
    routes: [
      GoRoute(path: '/login', builder: (context, state) => const LoginPage()),
      GoRoute(
        path: '/forgot-password',
        builder: (context, state) => const ForgotPasswordPage(),
      ),
      GoRoute(
        path: '/',
        builder: (context, state) => const RecoveryGate(child: HomeShell()),
        routes: [
          GoRoute(
            path: 'settings',
            builder: (context, state) => const SettingsPage(),
          ),
          GoRoute(
            path: 'profile',
            builder: (context, state) => const ProfilePage(),
          ),
          GoRoute(
            path: 'security',
            builder: (context, state) => const SecurityPage(),
          ),
          GoRoute(
            path: 'channels/:id',
            builder: (context, state) =>
                ChannelPage(channelId: state.pathParameters['id']!),
            routes: [
              GoRoute(
                path: 'messages/:mid',
                builder: (context, state) => MessagePage(
                  channelId: state.pathParameters['id']!,
                  messageId: state.pathParameters['mid']!,
                ),
              ),
            ],
          ),
          GoRoute(
            path: 'chats/:id',
            builder: (context, state) => ConversationChatPage(
              conversationId: state.pathParameters['id']!,
            ),
          ),
        ],
      ),
    ],
  );
});
