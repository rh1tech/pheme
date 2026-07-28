import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/providers.dart';
import '../core/snackbar.dart';
import '../core/server_address.dart';
import '../core/validators.dart';
import '../l10n/app_localizations.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/brand_logo.dart';
import '../widgets/password_strength_bar.dart';

const _resendSeconds = 120;

/// Password reset by emailed code: request a code, then enter it with a new
/// password. On success the user is logged in and the router redirects away.
class ForgotPasswordPage extends ConsumerStatefulWidget {
  const ForgotPasswordPage({super.key});

  @override
  ConsumerState<ForgotPasswordPage> createState() => _ForgotPasswordPageState();
}

class _ForgotPasswordPageState extends ConsumerState<ForgotPasswordPage> {
  final _email = TextEditingController();
  final _code = TextEditingController();
  final _password = TextEditingController();

  /// The server, here for the same reason it is on the sign-in form: a reset code is sent by a
  /// SERVER, and somebody who has never signed in on this device has not told the app which one.
  /// Seeded from whatever the install already points at.
  late final _server = TextEditingController(
    text: ref.read(initialAppStateProvider).savedBaseUrl ?? '',
  );
  bool _codeSent = false;
  bool _loading = false;
  int _cooldown = 0;
  Timer? _timer;

  @override
  void dispose() {
    _timer?.cancel();
    _email.dispose();
    _code.dispose();
    _password.dispose();
    _server.dispose();
    super.dispose();
  }

  void _startCooldown() {
    _timer?.cancel();
    setState(() => _cooldown = _resendSeconds);
    _timer = Timer.periodic(const Duration(seconds: 1), (t) {
      if (!mounted) return;
      setState(() => _cooldown--);
      if (_cooldown <= 0) t.cancel();
    });
  }

  Future<void> _requestCode() async {
    if (!_email.text.contains('@')) return;
    final l10n = context.l10n;
    if (!isValidServerUrl(_server.text)) {
      notifyError(context, l10n.t('settings.serverInvalid'));
      return;
    }
    FocusScope.of(context).unfocus();

    // Before the request, not after: a reset code has to be asked of the right server, and asking
    // the previous one would leak the email address to a host the user did not choose.
    await ref
        .read(settingsControllerProvider.notifier)
        .setBaseUrl(_server.text.trim());
    if (!mounted) return;

    setState(() => _loading = true);
    try {
      await ref.read(repositoryProvider).forgotPassword(_email.text.trim());
      if (mounted) {
        setState(() => _codeSent = true);
        _startCooldown();
      }
    } catch (e) {
      if (mounted) {
        notifyError(context, context.l10n.t('auth.requestFailed'), e);
      }
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _resend() async {
    if (_cooldown > 0 || _loading) return;
    setState(() => _loading = true);
    try {
      await ref.read(repositoryProvider).forgotPassword(_email.text.trim());
      _startCooldown();
    } catch (e) {
      if (mounted) {
        notifyError(context, context.l10n.t('auth.requestFailed'), e);
      }
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _submitReset() async {
    if (_code.text.trim().length != 6 ||
        !isPasswordAcceptable(_password.text)) {
      return;
    }
    FocusScope.of(context).unfocus();
    setState(() => _loading = true);
    try {
      await ref
          .read(authControllerProvider.notifier)
          .resetPassword(_email.text.trim(), _code.text.trim(), _password.text);
      if (mounted) notifySuccess(context, context.l10n.t('auth.resetDone'));
      // Router redirect handles navigation on auth change.
    } catch (e) {
      if (mounted) {
        notifyError(context, context.l10n.t('auth.requestFailed'), e);
      }
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return AdaptiveScaffold(
      body: SafeArea(
        child: Center(
          child: SingleChildScrollView(
            padding: const EdgeInsets.all(24),
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 420),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  const Center(child: BrandLogo(size: 40)),
                  const SizedBox(height: 28),
                  AdaptiveCard(
                    padding: const EdgeInsets.all(20),
                    child: _codeSent ? _buildReset(l10n) : _buildRequest(l10n),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildRequest(AppLocalizations l10n) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          l10n.t('auth.forgotTitle'),
          textAlign: TextAlign.center,
          style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w600),
        ),
        const SizedBox(height: 8),
        Text(
          l10n.t('auth.forgotSubtitle'),
          textAlign: TextAlign.center,
          style: Theme.of(context).textTheme.bodySmall,
        ),
        const SizedBox(height: 16),
        AdaptiveTextField(
          controller: _email,
          autofocus: true,
          keyboardType: TextInputType.emailAddress,
          autofillHints: const [AutofillHints.email],
          label: l10n.t('auth.email'),
          onSubmitted: (_) => _requestCode(),
        ),
        const SizedBox(height: 12),
        ServerFormField(controller: _server, enabled: !_loading),
        const SizedBox(height: 16),
        AdaptiveButton.filled(
          onPressed: _loading ? null : _requestCode,
          child: _loading
              ? const AdaptiveProgress(size: 20)
              : Text(l10n.t('auth.sendCode')),
        ),
        const SizedBox(height: 8),
        AdaptiveButton.text(
          onPressed: _loading ? null : () => context.pop(),
          child: Text(l10n.t('auth.backToSignIn')),
        ),
      ],
    );
  }

  Widget _buildReset(AppLocalizations l10n) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          l10n.t('auth.resetTitle'),
          style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w600),
        ),
        const SizedBox(height: 8),
        Text(
          l10n
              .t('auth.verifySubtitle')
              .replaceAll('{email}', _email.text.trim()),
          style: Theme.of(context).textTheme.bodySmall,
        ),
        const SizedBox(height: 16),
        AdaptiveTextField(
          controller: _code,
          autofocus: true,
          keyboardType: TextInputType.number,
          maxLength: 6,
          textAlign: TextAlign.center,
          style: const TextStyle(fontSize: 24, letterSpacing: 8),
          onChanged: (_) => setState(() {}),
        ),
        const SizedBox(height: 12),
        AdaptiveTextField(
          controller: _password,
          obscureText: true,
          label: l10n.t('auth.newPassword'),
          onChanged: (_) => setState(() {}),
        ),
        PasswordStrengthBar(password: _password.text),
        const SizedBox(height: 16),
        AdaptiveButton.filled(
          onPressed:
              _loading ||
                  _code.text.trim().length != 6 ||
                  !isPasswordAcceptable(_password.text)
              ? null
              : _submitReset,
          child: _loading
              ? const AdaptiveProgress(size: 20)
              : Text(l10n.t('auth.resetAction')),
        ),
        const SizedBox(height: 8),
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            AdaptiveButton.text(
              onPressed: _loading ? null : () => context.pop(),
              child: Text(l10n.t('auth.backToSignIn')),
            ),
            AdaptiveButton.text(
              onPressed: _cooldown > 0 || _loading ? null : _resend,
              child: Text(
                _cooldown > 0
                    ? l10n
                          .t('auth.resendIn')
                          .replaceAll('{seconds}', '$_cooldown')
                    : l10n.t('auth.resend'),
              ),
            ),
          ],
        ),
      ],
    );
  }
}
