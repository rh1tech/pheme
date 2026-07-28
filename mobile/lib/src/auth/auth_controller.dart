import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../chat/chat_providers.dart';
import '../chat/safety_pin_store.dart';
import '../crypto/mls_device.dart';
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

  /// Tells the server this device's MLS identity is finished, so it stops being claimed into groups.
  Future<void> _retireThisDevice() async {
    try {
      final deviceId = await loadMlsDeviceId(const FlutterSecureStorage());
      if (deviceId == null || deviceId.isEmpty) return;
      await ref.read(repositoryProvider).terminateDevice(deviceId);
    } on Object catch (e) {
      // A sign-out must complete. An identity left standing is the bug this exists to prevent, but
      // refusing to sign somebody out because the server was unreachable is a worse one.
      //
      // Said out loud, though. Swallowing this silently cost an afternoon: the identities in the
      // directory looked wrong, there was no way to tell whether the call had failed or never been
      // made, and the only evidence either way was on the server. If a leaf outlives its device
      // again, this line is what distinguishes "the request failed" from "we never asked".
      debugPrint('pheme/auth: could not retire this device on sign-out: $e');
    }
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

  /// Signs out, and destroys this device's encryption keys along with every message they decrypted.
  ///
  /// The keys and the plaintext cache are exactly what the end-to-end encryption exists to protect.
  /// Leaving them behind after signing out would mean the next person to pick up the phone could read
  /// the conversations — which would make the encryption a decoration. There is no way to recover
  /// them afterwards except from the passphrase-protected backup, and that is the point of it.
  ///
  /// The wipe runs BEFORE the tokens are cleared, because it is the part that matters: a failure to
  /// clear a token logs you out anyway, whereas a failure to wipe leaves the messages readable.
  Future<void> logout() async {
    // Retire this device on the SERVER first, while there is still a token to do it with and a
    // device id to name it by — wipeLocalKeys() destroys both.
    //
    // Without this, signing out wiped the private keys and left the identity standing in the
    // directory, still publishing claimable KeyPackages. Every group formed afterwards added that
    // dead leaf and encrypted to it, and it could never answer: the keys were gone. Two accounts
    // swapped between two handsets in an afternoon left six leaves in a two-person chat, four of
    // them corpses — and the one device that actually needed to read the message was not among
    // them, so the recipient got "Not available on this device".
    //
    // Best-effort, and deliberately not the full MlsService.terminateDevice: that also prunes the
    // leaf from every conversation, which needs a working session and a network, and a sign-out
    // must not hang on either. The tombstone is what co-members read, and this writes it.
    await _retireThisDevice();

    await ref.read(mlsServiceProvider).wipeLocalKeys();
    // The message envelopes are chat data too — wiped alongside the keys and bodies (which
    // wipeLocalKeys takes) so nothing readable about the conversations is left on the device.
    await ref.read(chatEnvelopeCacheProvider).wipe();
    await ref.read(safetyPinStoreProvider).wipe();
    await ref.read(lastSeenStoreProvider).wipe();
    // The push registration goes too. It is not chat data, which is why it was not in this list and
    // why the omission was invisible — but it names an ACCOUNT, and leaving it behind hands the next
    // person to sign in on this handset the last one's device row. See
    // SettingsStore.clearDeviceRegistration.
    await ref.read(settingsStoreProvider).clearDeviceRegistration();
    await ref.read(tokenStoreProvider).clear();
    state = const AuthState();
  }

  /// Called by the API client when a refresh fails irrecoverably.
  void onSessionExpired() {
    if (state.isAuthenticated) state = const AuthState();
  }
}
