import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/providers.dart';
import '../core/snackbar.dart';
import '../core/validators.dart';
import '../core/server_address.dart';
import '../l10n/app_localizations.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/brand_logo.dart';
import '../widgets/password_strength_bar.dart';

const _resendSeconds = 120;

/// Sign-in / register screen. Registration is two-step: submitting credentials
/// emails a 6-digit code, then the user confirms it here to create the account.
/// On success the router redirect navigates away automatically.
class LoginPage extends ConsumerStatefulWidget {
  const LoginPage({super.key});

  @override
  ConsumerState<LoginPage> createState() => _LoginPageState();
}

class _LoginPageState extends ConsumerState<LoginPage> {
  final _email = TextEditingController();
  final _password = TextEditingController();
  final _code = TextEditingController();

  /// The server, seeded from whatever this install already points at — the address a previous
  /// sign-in used, or the one a build was compiled with. Seeded, not assumed: it is a field the user
  /// can see and correct, and on a fresh install with no compiled default it starts empty and has to
  /// be filled in like any other.
  late final _server = TextEditingController(
    text: ref.read(initialAppStateProvider).savedBaseUrl ?? '',
  );
  final _formKey = GlobalKey<FormState>();
  bool _registerMode = false;
  bool _pendingVerify = false;
  bool _loading = false;
  int _cooldown = 0;
  Timer? _timer;

  @override
  void dispose() {
    _timer?.cancel();
    _email.dispose();
    _password.dispose();
    _code.dispose();
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

  Future<void> _submit() async {
    if (!_formKey.currentState!.validate()) return;
    FocusScope.of(context).unfocus();

    // The server first, and awaited: everything below is a request, and a request needs to know
    // where it is going. Sending credentials to the previous address and only then switching would
    // hand one server the password meant for another.
    await ref
        .read(settingsControllerProvider.notifier)
        .setBaseUrl(_server.text.trim());
    if (!mounted) return;

    setState(() => _loading = true);
    final auth = ref.read(authControllerProvider.notifier);
    final email = _email.text.trim();
    final password = _password.text;
    try {
      if (_registerMode) {
        await auth.register(email, password);
        if (mounted) {
          setState(() => _pendingVerify = true);
          _startCooldown();
        }
      } else {
        await auth.login(email, password);
        // Router redirect handles navigation on auth change.
      }
    } catch (e) {
      if (mounted) {
        notifyError(context, context.l10n.t('auth.requestFailed'), e);
      }
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _verify() async {
    if (_code.text.trim().length != 6) return;
    FocusScope.of(context).unfocus();
    setState(() => _loading = true);
    try {
      await ref
          .read(authControllerProvider.notifier)
          .verifyEmail(_email.text.trim(), _code.text.trim());
      // Router redirect handles navigation on auth change.
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
      await ref
          .read(authControllerProvider.notifier)
          .register(_email.text.trim(), _password.text);
      _startCooldown();
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
    return Scaffold(
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
                    child: _pendingVerify
                        ? _buildVerify(l10n)
                        : _buildCredentials(l10n),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildCredentials(AppLocalizations l10n) {
    return Form(
      key: _formKey,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            _registerMode
                ? l10n.t('auth.registerSubtitle')
                : l10n.t('auth.signInSubtitle'),
            textAlign: TextAlign.center,
            style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w600),
          ),
          const SizedBox(height: 16),
          AdaptiveTextFormField(
            controller: _email,
            autofocus: true,
            keyboardType: TextInputType.emailAddress,
            autofillHints: const [AutofillHints.email],
            label: l10n.t('auth.email'),
            validator: (v) =>
                (v == null || !v.contains('@')) ? l10n.t('auth.email') : null,
          ),
          const SizedBox(height: 12),
          AdaptiveTextFormField(
            controller: _password,
            obscureText: true,
            autofillHints: const [AutofillHints.password],
            label: l10n.t('auth.password'),
            onChanged: _registerMode ? (_) => setState(() {}) : null,
            onFieldSubmitted: (_) => _submit(),
            validator: (v) {
              final value = v ?? '';
              if (_registerMode) {
                return isPasswordAcceptable(value)
                    ? null
                    : l10n.t('auth.passwordWeak');
              }
              return value.isEmpty ? l10n.t('auth.password') : null;
            },
          ),
          if (_registerMode) PasswordStrengthBar(password: _password.text),
          const SizedBox(height: 12),
          // The third field, on sign-in and registration alike. Which server you are talking to is
          // part of who you are signing in as — the same email exists on two Pheme instances and is
          // two different people.
          ServerFormField(controller: _server, enabled: !_loading),
          const SizedBox(height: 16),
          AdaptiveButton.filled(
            onPressed: _loading ? null : _submit,
            child: _loading
                ? const _Spinner()
                : Text(
                    _registerMode
                        ? l10n.t('auth.register')
                        : l10n.t('auth.signIn'),
                  ),
          ),
          const SizedBox(height: 8),
          AdaptiveButton.text(
            onPressed: _loading
                ? null
                : () => setState(() => _registerMode = !_registerMode),
            child: Text(
              _registerMode
                  ? l10n.t('auth.haveAccount')
                  : l10n.t('auth.noAccount'),
            ),
          ),
          // Last, under the register link. It used to sit between the fields and the Sign in button,
          // interrupting the form on the way to the thing almost everybody came to press. Both of
          // these are ways OUT of signing in, so they belong together at the foot of the card.
          if (!_registerMode)
            AdaptiveButton.text(
              onPressed: _loading
                  ? null
                  : () => context.push('/forgot-password'),
              child: Text(l10n.t('auth.forgotPassword')),
            ),
        ],
      ),
    );
  }

  Widget _buildVerify(AppLocalizations l10n) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          l10n.t('auth.verifyTitle'),
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
          onChanged: (v) {
            setState(() {});
            if (v.trim().length == 6) _verify();
          },
        ),
        const SizedBox(height: 12),
        AdaptiveButton.filled(
          onPressed: _loading || _code.text.trim().length != 6 ? null : _verify,
          child: _loading
              ? const _Spinner()
              : Text(l10n.t('auth.verifyAction')),
        ),
        const SizedBox(height: 8),
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            AdaptiveButton.text(
              onPressed: _loading
                  ? null
                  : () => setState(() => _pendingVerify = false),
              child: Text(l10n.t('auth.back')),
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

class _Spinner extends StatelessWidget {
  const _Spinner();

  @override
  Widget build(BuildContext context) => const AdaptiveProgress(size: 20);
}
