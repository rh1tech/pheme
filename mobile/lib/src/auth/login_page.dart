import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/deep_links.dart';
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
  final _invite = TextEditingController();

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

  /// Whether the server this form points at requires an invitation. Unknown until asked, and
  /// treated as "open" until then: showing a field nobody can fill would block signup on a
  /// server that never wanted one, and an invite-only server still refuses the registration
  /// itself, so guessing wrong this way costs a clear error rather than a locked-out user.
  bool _inviteOnly = false;

  /// The server address the answer above belongs to. An answer is about ONE server, and the
  /// address is a field the user can edit, so it has to be re-asked when they do.
  String? _inviteOnlyFor;

  /// The server's verdict on the code currently typed, and the code it was about — kept
  /// together so a verdict is never shown against a code that has since been edited.
  String? _inviteReason;
  String? _inviteCheckedCode;

  /// Waits for typing to settle before spending a request on the code.
  Timer? _inviteDebounce;

  @override
  void initState() {
    super.initState();
    // An invitation link may already be waiting — the app can have been cold-started BY it, in
    // which case it arrived before this screen existed. Taken after the first frame so the setState
    // below has a mounted element to rebuild, and so the register-mode switch is a visible change
    // rather than the initial state of a form the user never chose.
    WidgetsBinding.instance.addPostFrameCallback((_) => _takeInviteLink());
  }

  /// Claims a pending invitation link, filling the form in from it.
  void _takeInviteLink() {
    final link = ref
        .read(deepLinkControllerProvider.notifier)
        .consume<InviteLink>();
    if (link == null || !mounted) return;
    setState(() {
      _registerMode = true;
      _invite.text = link.code;
      // The invitation names the server it is for, and it is almost never the one a fresh install
      // is pointing at. Filling it in is the whole reason the link carries it — but it stays an
      // editable field rather than a silent switch, because this is the address credentials are
      // about to be sent to.
      if (link.server != null) _server.text = link.server!;
    });
    unawaited(_loadRegistrationMode());
    unawaited(_checkInvite());
  }

  @override
  void dispose() {
    _timer?.cancel();
    _inviteDebounce?.cancel();
    _email.dispose();
    _password.dispose();
    _code.dispose();
    _invite.dispose();
    _server.dispose();
    super.dispose();
  }

  /// Asks the server whether registration is invited-only, once per address.
  ///
  /// Deliberately fired when the user turns to the register form rather than on every
  /// keystroke in the address field: it is one unauthenticated GET, and it carries nothing
  /// but the question.
  Future<void> _loadRegistrationMode() async {
    final base = _server.text.trim();
    if (base.isEmpty || base == _inviteOnlyFor) return;
    try {
      await ref.read(settingsControllerProvider.notifier).setBaseUrl(base);
      final inviteOnly = await ref
          .read(repositoryProvider)
          .registrationIsInviteOnly();
      if (!mounted) return;
      setState(() {
        _inviteOnly = inviteOnly;
        _inviteOnlyFor = base;
      });
    } on Object {
      // A server too old to answer, or one briefly unreachable. Leave the field hidden and
      // let the register attempt return the real verdict.
      if (!mounted) return;
      setState(() {
        _inviteOnly = false;
        _inviteOnlyFor = base;
      });
    }
  }

  /// The message for a rejected invitation. Written out rather than assembled from the
  /// server's word, so a reason the server grows later shows the generic line instead of a
  /// missing translation key.
  String _inviteRejection(AppLocalizations l10n, String reason) =>
      switch (reason) {
        'used' => l10n.t('auth.inviteUsed'),
        'revoked' => l10n.t('auth.inviteRevoked'),
        'expired' => l10n.t('auth.inviteExpired'),
        _ => l10n.t('auth.inviteUnknown'),
      };

  /// Checks the pasted code so a spent link says so before the form is filled in.
  Future<void> _checkInvite() async {
    final code = _invite.text.trim();
    if (code.isEmpty || code == _inviteCheckedCode) return;
    try {
      final reason = await ref.read(repositoryProvider).checkInvite(code);
      if (!mounted) return;
      setState(() {
        _inviteCheckedCode = code;
        _inviteReason = reason;
      });
    } on Object {
      // Silence, not a verdict: /register decides, and calling a good invitation bad because
      // the check request failed would be worse than saying nothing.
    }
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
    if (_formKey.currentState?.validate() != true) return;
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
        await auth.register(email, password, invite: _invite.text.trim());
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
          .register(
            _email.text.trim(),
            _password.text,
            invite: _invite.text.trim(),
          );
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
    // A link that arrives while this screen is already open — the app was in the background and the
    // invitation was tapped — has no post-frame callback to catch it, so it is claimed here too.
    ref.listen(deepLinkControllerProvider, (_, next) {
      if (next is InviteLink) _takeInviteLink();
    });
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
          if (_registerMode && _inviteOnly) ...[
            const SizedBox(height: 12),
            AdaptiveTextFormField(
              controller: _invite,
              label: l10n.t('auth.invite'),
              onChanged: (_) {
                setState(() {});
                _inviteDebounce?.cancel();
                _inviteDebounce = Timer(
                  const Duration(milliseconds: 400),
                  _checkInvite,
                );
              },
              onFieldSubmitted: (_) => _submit(),
              validator: (v) {
                final code = (v ?? '').trim();
                if (code.isEmpty) return l10n.t('auth.inviteRequired');
                final reason = code == _inviteCheckedCode
                    ? _inviteReason
                    : null;
                return reason == null ? null : _inviteRejection(l10n, reason);
              },
            ),
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: Text(
                _inviteHint(l10n),
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ),
          ],
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
                : () {
                    setState(() => _registerMode = !_registerMode);
                    if (_registerMode) unawaited(_loadRegistrationMode());
                  },
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

  /// The line under the invite field: what we know about the code as it stands.
  String _inviteHint(AppLocalizations l10n) {
    final code = _invite.text.trim();
    if (code.isEmpty) return l10n.t('auth.inviteRequired');
    if (code != _inviteCheckedCode) return l10n.t('auth.inviteChecking');
    final reason = _inviteReason;
    return reason == null
        ? l10n.t('auth.inviteValid')
        : _inviteRejection(l10n, reason);
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
