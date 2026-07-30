import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import 'admin/admin_channel_page.dart';
import 'admin/admin_channels_page.dart';
import 'admin/admin_comments_page.dart';
import 'admin/admin_home_page.dart';
import 'admin/admin_invites_page.dart';
import 'admin/admin_users_page.dart';
import 'auth/forgot_password_page.dart';
import 'auth/login_page.dart';
import 'channels/channel_page.dart';
import 'channels/channel_settings_page.dart';
import 'channels/message_page.dart';
import 'chat/conversation_chat_page.dart';
import 'core/providers.dart';
import 'crypto/recovery_gate.dart';
import 'home_shell.dart';
import 'models/models.dart';
import 'profile/profile_page.dart';
import 'profile/user_profile_page.dart';
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
      // The panel is admin-only. Checked on every navigation rather than once at the entry row,
      // because the role can be taken away mid-session — another admin demotes this account — and
      // a deep link straight to /admin/users must not bypass whatever the entry row decided.
      if (loc.startsWith('/admin') &&
          !ref.read(authControllerProvider).isAdmin) {
        return '/';
      }
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
          // The admin panel. Guarded by the redirect below as well as by the server, which answers
          // 403 to every one of these endpoints for an account without the role — the guard here is
          // so a non-admin sees the settings screen rather than six screens of load failures.
          GoRoute(
            path: 'admin',
            builder: (context, state) => const AdminHomePage(),
            routes: [
              GoRoute(
                path: 'users',
                builder: (context, state) => const AdminUsersPage(),
              ),
              GoRoute(
                path: 'channels',
                builder: (context, state) => const AdminChannelsPage(),
                routes: [
                  GoRoute(
                    path: ':id',
                    builder: (context, state) => AdminChannelPage(
                      channelId: state.pathParameters['id']!,
                    ),
                  ),
                ],
              ),
              GoRoute(
                path: 'comments',
                builder: (context, state) => const AdminCommentsPage(),
              ),
              GoRoute(
                path: 'invites',
                builder: (context, state) => const AdminInvitesPage(),
              ),
            ],
          ),
          // Somebody else's profile. The member row it was opened from is passed as `extra` so the
          // screen has a name and a face to draw before the request answers — and something to
          // fall back on if it never does.
          GoRoute(
            path: 'users/:id',
            builder: (context, state) => UserProfilePage(
              userId: state.pathParameters['id']!,
              fallback: state.extra as PublicProfile?,
            ),
          ),
          GoRoute(
            path: 'channels/:id',
            builder: (context, state) =>
                ChannelPage(channelId: state.pathParameters['id']!),
            routes: [
              // A screen rather than a sheet, because it is a form with a Save and it is the one
              // thing here that changes the channel for everybody who reads it.
              GoRoute(
                path: 'settings',
                builder: (context, state) =>
                    ChannelSettingsPage(channelId: state.pathParameters['id']!),
              ),
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
