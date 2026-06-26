import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/jwt.dart';
import '../core/providers.dart';
import '../core/token_store.dart';

class AuthState {
  const AuthState({this.userId, this.role});

  final String? userId;
  final String? role;

  bool get isAuthenticated => userId != null;
  bool get isAdmin => role == 'admin';
}

/// Owns the authenticated identity. Seeds from any persisted tokens at startup
/// and reacts to session expiry signalled by the Dio interceptor.
class AuthController extends Notifier<AuthState> {
  @override
  AuthState build() {
    final tokens = ref.read(tokenStoreProvider).current;
    if (tokens == null) return const AuthState();
    return AuthState(
      userId: decodeUserId(tokens.accessToken),
      role: decodeRole(tokens.accessToken),
    );
  }

  Future<void> login(String email, String password) async {
    final res = await ref.read(repositoryProvider).login(email, password);
    await _apply(res.accessToken, res.refreshToken, res.userId, res.role);
  }

  /// Starts registration — the server emails a verification code. The account
  /// is created (and the user logged in) once [verifyEmail] confirms the code.
  Future<void> register(String email, String password) async {
    await ref.read(repositoryProvider).register(email, password);
  }

  Future<void> verifyEmail(String email, String code) async {
    final res = await ref.read(repositoryProvider).verifyEmail(email, code);
    await _apply(res.accessToken, res.refreshToken, res.userId, res.role);
  }

  Future<void> resetPassword(
    String email,
    String code,
    String newPassword,
  ) async {
    final res = await ref
        .read(repositoryProvider)
        .resetPassword(email, code, newPassword);
    await _apply(res.accessToken, res.refreshToken, res.userId, res.role);
  }

  Future<void> _apply(
    String access,
    String refresh,
    String userId,
    String role,
  ) async {
    await ref
        .read(tokenStoreProvider)
        .save(Tokens(accessToken: access, refreshToken: refresh));
    state = AuthState(userId: userId, role: role);
  }

  Future<void> logout() async {
    await ref.read(tokenStoreProvider).clear();
    state = const AuthState();
  }

  /// Called by the API client when a refresh fails irrecoverably.
  void onSessionExpired() {
    if (state.isAuthenticated) state = const AuthState();
  }
}
