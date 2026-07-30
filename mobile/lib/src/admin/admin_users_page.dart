import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';
import '../core/snackbar.dart';
import '../core/validators.dart';
import '../l10n/app_localizations.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/glass/glass.dart';
import '../widgets/password_strength_bar.dart';
import 'admin_models.dart';
import 'admin_providers.dart';
import 'widgets/admin_ui.dart';

/// Account administration: search, promote, block, disable, reset a password, delete.
class AdminUsersPage extends ConsumerWidget {
  const AdminUsersPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = context.l10n;
    final admin = ref.read(adminRepositoryProvider);
    // Who is signed in, so their own row can be marked and the actions that would lock them out of
    // their own panel — demoting or blocking themselves — are not offered.
    final me = ref.watch(authControllerProvider).userId;

    return AdminListScreen<AdminUser>(
      title: l10n.t('admin.navUsers'),
      searchPlaceholder: l10n.t('admin.searchUsers'),
      emptyLabel: l10n.t('admin.noUsers'),
      fetch: (query, page) =>
          admin.listUsers(query: query, page: page, limit: adminPageLimit),
      primaryAction: (context, reload) => AdminAction(
        icon: Icons.person_add_alt,
        iosIcon: CupertinoIcons.person_add,
        label: l10n.t('admin.addUser'),
        onPressed: () => _createUser(context, ref, reload),
      ),
      rowBuilder: (context, user, reload) =>
          _UserRow(user: user, isSelf: user.id == me, onChanged: reload),
    );
  }

  Future<void> _createUser(
    BuildContext context,
    WidgetRef ref,
    VoidCallback reload,
  ) async {
    final l10n = context.l10n;
    final created = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      builder: (_) => const _CreateUserSheet(),
    );
    if (created != true || !context.mounted) return;
    notifySuccess(context, l10n.t('admin.userCreated'));
    reload();
  }
}

/// One account, with everything that can be done to it behind the row's menu.
class _UserRow extends ConsumerStatefulWidget {
  const _UserRow({
    required this.user,
    required this.isSelf,
    required this.onChanged,
  });

  final AdminUser user;
  final bool isSelf;
  final VoidCallback onChanged;

  @override
  ConsumerState<_UserRow> createState() => _UserRowState();
}

class _UserRowState extends ConsumerState<_UserRow> {
  bool _busy = false;

  AdminUser get _user => widget.user;

  /// Runs an action, reports it, and reloads the list from the server.
  ///
  /// Reloading rather than patching the row locally: several of these change more than the field
  /// they name — deleting a user removes their channels, blocking one ends their sessions — and a
  /// list that guessed would show a stale count next to a fresh status.
  Future<void> _run(
    Future<void> Function() action, {
    required String successKey,
    required String failKey,
  }) async {
    setState(() => _busy = true);
    final l10n = context.l10n;
    try {
      await action();
      if (!mounted) return;
      notifySuccess(context, l10n.t(successKey));
      widget.onChanged();
    } on Object catch (e) {
      if (!mounted) return;
      notifyError(context, l10n.t(failKey), e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _setRole(String role) => _run(
    () => ref.read(adminRepositoryProvider).updateUser(_user.id, role: role),
    successKey: 'admin.userUpdated',
    failKey: 'admin.updateFailed',
  );

  Future<void> _setStatus(String status) => _run(
    () =>
        ref.read(adminRepositoryProvider).updateUser(_user.id, status: status),
    successKey: 'admin.userUpdated',
    failKey: 'admin.updateFailed',
  );

  Future<void> _delete() async {
    final l10n = context.l10n;
    final ok = await showAdaptiveConfirm(
      context,
      title: l10n.t('admin.deleteUser'),
      message: l10n
          .t('admin.deleteUserConfirm')
          .replaceAll('{name}', _user.email),
      confirmLabel: l10n.t('common.delete'),
      cancelLabel: l10n.t('common.cancel'),
      isDestructive: true,
    );
    if (!ok || !mounted) return;
    await _run(
      () => ref.read(adminRepositoryProvider).deleteUser(_user.id),
      successKey: 'admin.userDeleted',
      failKey: 'admin.deleteFailed',
    );
  }

  Future<void> _resetPassword() async {
    final password = await showModalBottomSheet<String>(
      context: context,
      isScrollControlled: true,
      builder: (_) => _ResetPasswordSheet(email: _user.email),
    );
    if (password == null || password.isEmpty || !mounted) return;
    await _run(
      () => ref
          .read(adminRepositoryProvider)
          .resetUserPassword(_user.id, password),
      successKey: 'admin.passwordReset',
      failKey: 'admin.resetPasswordFailed',
    );
  }

  /// What may be done to this account, given who is asking.
  List<GlassMenuAction> _menuActions(AppLocalizations l10n) {
    return <GlassMenuAction>[
      // Never offered on your own row. An admin who demotes or blocks themselves cannot undo it
      // from here — the next request is a 403 — and would need another admin, or the server's
      // allowlist, to get back in.
      if (!widget.isSelf)
        GlassMenuAction(
          label: _user.isAdmin
              ? l10n.t('admin.makeUser')
              : l10n.t('admin.makeAdmin'),
          icon: _user.isAdmin ? Icons.person_outline : Icons.shield_outlined,
          onSelected: () => _setRole(_user.isAdmin ? 'user' : 'admin'),
        ),
      if (!widget.isSelf)
        GlassMenuAction(
          label: _user.isBlocked
              ? l10n.t('admin.unblock')
              : l10n.t('admin.block'),
          icon: _user.isBlocked ? Icons.lock_open_outlined : Icons.block,
          destructive: !_user.isBlocked,
          onSelected: () => _setStatus(_user.isBlocked ? 'active' : 'blocked'),
        ),
      if (!widget.isSelf)
        GlassMenuAction(
          label: _user.isDisabled
              ? l10n.t('admin.enable')
              : l10n.t('admin.disable'),
          icon: _user.isDisabled
              ? Icons.toggle_on_outlined
              : Icons.toggle_off_outlined,
          onSelected: () =>
              _setStatus(_user.isDisabled ? 'active' : 'disabled'),
        ),
      GlassMenuAction(
        label: l10n.t('admin.resetPassword'),
        icon: Icons.password_outlined,
        onSelected: _resetPassword,
      ),
      if (!widget.isSelf)
        GlassMenuAction(
          label: l10n.t('common.delete'),
          icon: Icons.delete_outline,
          destructive: true,
          onSelected: _delete,
        ),
    ];
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final scheme = Theme.of(context).colorScheme;

    return ListTile(
      title: Row(
        children: [
          Flexible(
            child: Text(
              _user.email,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
          if (widget.isSelf) ...[
            const SizedBox(width: 6),
            Text(
              '(${l10n.t('admin.you')})',
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ],
        ],
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 4),
        child: Wrap(
          spacing: 6,
          runSpacing: 4,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            if (_user.isAdmin)
              AdminBadge(
                label: l10n.t('admin.roleAdmin'),
                color: scheme.primary,
              ),
            if (_user.isBlocked)
              AdminBadge(
                label: l10n.t('admin.statusBlocked'),
                color: scheme.error,
              ),
            if (_user.isDisabled)
              AdminBadge(label: l10n.t('admin.statusDisabled')),
            Text(
              l10n
                  .t('admin.channelsCount')
                  .replaceAll('{count}', '${_user.channelCount}'),
              style: Theme.of(context).textTheme.bodySmall,
            ),
            Text(
              adminDate(_user.createdAt),
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ],
        ),
      ),
      trailing: _busy
          ? const SizedBox.square(
              dimension: GlassMetrics.minTapTarget,
              child: Center(child: AdaptiveProgress(size: 18)),
            )
          : GlassMenuButton(
              actions: _menuActions(l10n),
              semanticLabel: l10n.t('admin.actions'),
            ),
    );
  }
}

/// The form for adding an account directly — no email verification, no invitation.
class _CreateUserSheet extends ConsumerStatefulWidget {
  const _CreateUserSheet();

  @override
  ConsumerState<_CreateUserSheet> createState() => _CreateUserSheetState();
}

class _CreateUserSheetState extends ConsumerState<_CreateUserSheet> {
  final _email = TextEditingController();
  final _password = TextEditingController();
  final _formKey = GlobalKey<FormState>();
  bool _admin = false;
  bool _saving = false;

  @override
  void dispose() {
    _email.dispose();
    _password.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (_formKey.currentState?.validate() != true) return;
    setState(() => _saving = true);
    final l10n = context.l10n;
    try {
      await ref
          .read(adminRepositoryProvider)
          .createUser(
            email: _email.text.trim(),
            password: _password.text,
            role: _admin ? 'admin' : 'user',
          );
      if (mounted) Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      notifyError(context, l10n.t('admin.createUserFailed'), e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return _SheetShell(
      title: l10n.t('admin.addUser'),
      child: Form(
        key: _formKey,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          mainAxisSize: MainAxisSize.min,
          children: [
            AdaptiveTextFormField(
              controller: _email,
              autofocus: true,
              keyboardType: TextInputType.emailAddress,
              label: l10n.t('auth.email'),
              validator: (v) =>
                  (v == null || !v.contains('@')) ? l10n.t('auth.email') : null,
            ),
            const SizedBox(height: 12),
            AdaptiveTextFormField(
              controller: _password,
              obscureText: true,
              label: l10n.t('auth.password'),
              onChanged: (_) => setState(() {}),
              validator: (v) => isPasswordAcceptable(v ?? '')
                  ? null
                  : l10n.t('auth.passwordWeak'),
            ),
            PasswordStrengthBar(password: _password.text),
            const SizedBox(height: 8),
            SwitchListTile.adaptive(
              contentPadding: EdgeInsets.zero,
              value: _admin,
              onChanged: (v) => setState(() => _admin = v),
              title: Text(l10n.t('admin.roleAdmin')),
              subtitle: Text(l10n.t('admin.roleAdminHint')),
            ),
            const SizedBox(height: 12),
            AdaptiveButton.filled(
              onPressed: _saving ? null : _submit,
              child: _saving
                  ? const AdaptiveProgress(size: 20)
                  : Text(l10n.t('admin.addUser')),
            ),
          ],
        ),
      ),
    );
  }
}

/// Sets a new password on somebody else's account. Pops the password, or null if cancelled.
class _ResetPasswordSheet extends StatefulWidget {
  const _ResetPasswordSheet({required this.email});

  final String email;

  @override
  State<_ResetPasswordSheet> createState() => _ResetPasswordSheetState();
}

class _ResetPasswordSheetState extends State<_ResetPasswordSheet> {
  final _password = TextEditingController();
  final _formKey = GlobalKey<FormState>();

  @override
  void dispose() {
    _password.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return _SheetShell(
      title: l10n.t('admin.resetPassword'),
      subtitle: widget.email,
      child: Form(
        key: _formKey,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          mainAxisSize: MainAxisSize.min,
          children: [
            AdaptiveTextFormField(
              controller: _password,
              autofocus: true,
              obscureText: true,
              label: l10n.t('auth.newPassword'),
              onChanged: (_) => setState(() {}),
              validator: (v) => isPasswordAcceptable(v ?? '')
                  ? null
                  : l10n.t('auth.passwordWeak'),
            ),
            PasswordStrengthBar(password: _password.text),
            const SizedBox(height: 12),
            AdaptiveButton.filled(
              onPressed: () {
                if (_formKey.currentState?.validate() != true) return;
                Navigator.of(context).pop(_password.text);
              },
              child: Text(l10n.t('admin.resetPassword')),
            ),
          ],
        ),
      ),
    );
  }
}

/// The padding, the title and the keyboard inset every sheet on these screens needs.
class _SheetShell extends StatelessWidget {
  const _SheetShell({required this.title, required this.child, this.subtitle});

  final String title;
  final String? subtitle;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return SafeArea(
      child: Padding(
        // The keyboard's height as bottom padding: these sheets are all forms, and a submit button
        // under the keyboard is a submit button that does not exist.
        padding: EdgeInsets.fromLTRB(
          20,
          16,
          20,
          16 + MediaQuery.viewInsetsOf(context).bottom,
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              title,
              style: theme.textTheme.titleMedium?.copyWith(
                fontWeight: FontWeight.w600,
              ),
            ),
            if (subtitle != null) ...[
              const SizedBox(height: 2),
              Text(
                subtitle!,
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ],
            const SizedBox(height: 16),
            child,
          ],
        ),
      ),
    );
  }
}
