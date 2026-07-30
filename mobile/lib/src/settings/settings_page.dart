import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/app_config.dart';
import '../chat/chat_providers.dart';
import '../core/providers.dart';
import '../core/server_address.dart';
import '../core/snackbar.dart';
import '../crypto/recovery_gate.dart';
import '../l10n/app_localizations.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/glass/glass.dart';
import 'settings_controller.dart';

/// App preferences: theme, language, API server, device push status and logout.
class SettingsPage extends ConsumerStatefulWidget {
  const SettingsPage({super.key});

  @override
  ConsumerState<SettingsPage> createState() => _SettingsPageState();
}

class _SettingsPageState extends ConsumerState<SettingsPage> {
  /// How much this account's notifications may reveal: 'preview', 'sender' or 'generic'.
  ///
  /// It lives here and not in [SettingsState] because it is a property of the ACCOUNT, not
  /// of this handset: it has to hold on every device the user signs in on, and SettingsState
  /// is device-local secure storage that never reaches the server. Null while loading, and
  /// if the load fails — the control stays hidden rather than showing a default that might be
  /// the opposite of the truth and silently rewriting it on the first tap.
  ///
  /// This was a BOOLEAN, and that was a bug rather than a simplification: a switch can only
  /// express two of the three options, so it wrote 'sender' or 'generic' and there was no way
  /// to ask for a message preview at all. Turning it on and getting no preview was the correct
  /// behaviour of the wrong control.
  String? _privacy;

  /// The three options, in order of how much they reveal. Declared once so the Material and
  /// Cupertino trees cannot come to disagree about what the choices are — which is precisely how
  /// the previous control ended up unable to express one of them.
  static const _privacyOptions = <(String, String, String)>[
    ('preview', 'settings.previewMessage', 'settings.previewMessageHint'),
    ('sender', 'settings.previewSender', 'settings.previewSenderHint'),
    ('generic', 'settings.previewGeneric', 'settings.previewGenericHint'),
  ];

  @override
  void initState() {
    super.initState();
    _loadPrivacy();
  }

  Future<void> _loadPrivacy() async {
    try {
      final me = await ref.read(repositoryProvider).getMe();
      // Absent means the account predates the setting, which behaves as 'sender'.
      if (mounted) {
        setState(() => _privacy = me.notificationPrivacy ?? 'sender');
      }
    } on Object {
      // Leave it null: better no control than one showing a state we could not confirm.
    }
  }

  Future<void> _setPrivacy(String value) async {
    final previous = _privacy;
    // Optimistic: a privacy control that lags behind the finger feels broken. Rolled back below
    // if the server refuses, so it can never end up showing something the account does not say.
    setState(() => _privacy = value);
    try {
      await ref.read(repositoryProvider).updateMe(notificationPrivacy: value);
    } on Object {
      if (!mounted) return;
      setState(() => _privacy = previous);
      notifyError(
        context,
        context.l10n.t('settings.notificationPrivacyFailed'),
      );
    }
  }

  @override
  void dispose() {
    super.dispose();
  }

  /// Scans a server URL off a QR code and saves it.
  ///
  /// A self-hosted Pheme server is reached at an unlisted path prefix —
  /// `https://host.example/a7f3c91e4b2d` — which is long, case-sensitive and
  /// meaningless, so typing it off a screen is exactly the sort of thing people
  /// get wrong once and give up on. The self-host kit prints this QR for the
  /// operator to hand out (see deploy/self-host).
  ///
  /// Scanned raw: unlike a channel code there is no `ref` to extract, and a
  /// query parameter in a server URL is part of the address.
  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final settings = ref.watch(settingsControllerProvider);
    final controller = ref.read(settingsControllerProvider.notifier);
    final registered = ref.watch(deviceControllerProvider) != null;
    // Watched, not read once: another admin can take the role away mid-session, and the row should
    // go with it rather than lead to a screen that 403s.
    final isAdmin = ref.watch(authControllerProvider).isAdmin;
    // Read, not watched: nothing publishes a change, and this screen is rebuilt on every visit.
    final backupFailing = ref.read(mlsServiceProvider).backupHealth.failing;

    return AdaptiveScaffold(
      grouped: isCupertino(context),
      title: Text(l10n.t('settings.title')),
      body: isCupertino(context)
          ? _buildCupertino(
              l10n,
              settings,
              controller,
              registered,
              isAdmin,
              backupFailing,
            )
          : _buildMaterial(
              context,
              l10n,
              settings,
              controller,
              registered,
              isAdmin,
              backupFailing,
            ),
    );
  }

  // ---------------------------------------------------------------------------
  // Android (Material)
  // ---------------------------------------------------------------------------

  Widget _buildMaterial(
    BuildContext context,
    AppLocalizations l10n,
    SettingsState settings,
    SettingsController controller,
    bool registered,
    bool isAdmin,
    bool backupFailing,
  ) {
    return ListView(
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        _SectionHeader(title: l10n.t('settings.appearance')),
        // Every setting here is a row that states its current value and opens a chooser.
        //
        // They used to be the controls themselves — a dropdown here, a text field and a Save
        // button there, three radio rows for the lock screen — so the screen was a mix of widgets
        // at different heights, and reading off what a setting was CURRENTLY set to meant
        // interpreting a different control each time. The iOS tree already worked this way; this
        // is the Material one catching up, and the two now describe the same screen.
        _ValueRow(
          icon: Icons.brightness_6_outlined,
          title: l10n.t('settings.theme'),
          value: _themeLabel(l10n, settings.themeMode),
          onTap: () => _pickTheme(l10n, settings, controller),
        ),
        _ValueRow(
          icon: Icons.translate_outlined,
          title: l10n.t('settings.language'),
          value: _languageLabel(
            l10n,
            settings.locale?.languageCode ?? 'system',
          ),
          onTap: () => _pickLanguage(l10n, settings, controller),
        ),
        const Divider(height: 24),
        _SectionHeader(title: l10n.t('settings.server')),
        _ValueRow(
          icon: Icons.dns_outlined,
          title: l10n.t('settings.serverUrl'),
          value: _serverHost(settings.baseUrl),
          // Read-only, and the tap SHOWS it rather than edits it — see _ServerNote.
          onTap: () => showServerQr(context, ref),
        ),
        const _ServerNote(),
        const Divider(height: 24),
        _SectionHeader(title: l10n.t('settings.device')),
        ListTile(
          leading: Icon(
            registered
                ? Icons.notifications_active_outlined
                : Icons.notifications_off_outlined,
          ),
          title: Text(
            registered
                ? l10n.t('settings.deviceRegistered')
                : l10n.t('settings.deviceNotRegistered'),
          ),
        ),
        if (_privacy != null)
          _ValueRow(
            icon: Icons.lock_outline,
            title: l10n.t('settings.aboutLockScreen'),
            value: _privacyLabel(l10n, _privacy!),
            onTap: () => _pickPrivacy(l10n),
          ),
        const Divider(height: 24),
        _SectionHeader(title: l10n.t('settings.account')),
        ListTile(
          leading: const Icon(Icons.person_outline),
          title: Text(l10n.t('settings.profile')),
          trailing: const Icon(Icons.chevron_right),
          onTap: () => context.push('/profile'),
        ),
        ListTile(
          leading: const Icon(Icons.security_outlined),
          title: Text(l10n.t('security.menuItem')),
          trailing: const Icon(Icons.chevron_right),
          onTap: () => context.push('/security'),
        ),
        ListTile(
          leading: Icon(
            Icons.vpn_key_outlined,
            color: backupFailing ? Theme.of(context).colorScheme.error : null,
          ),
          title: Text(l10n.t('recovery.menuItem')),
          // The only place a failing backup is visible. Silence here is what let a real backup
          // go stale unnoticed until the device it lived on was replaced.
          subtitle: backupFailing
              ? Text(
                  l10n.t('recovery.backupFailing'),
                  style: TextStyle(color: Theme.of(context).colorScheme.error),
                )
              : null,
          trailing: const Icon(Icons.chevron_right),
          onTap: () => showRecoveryCodeSheet(context, ref),
        ),
        // The panel, for the accounts that have one. Absent rather than disabled for everybody
        // else: a row that exists and refuses is an invitation to wonder what is behind it, and
        // there is nothing an ordinary account could do with it anyway.
        if (isAdmin)
          ListTile(
            leading: const Icon(Icons.shield_outlined),
            title: Text(l10n.t('admin.title')),
            trailing: const Icon(Icons.chevron_right),
            onTap: () => context.push('/admin'),
          ),
        ListTile(
          leading: Icon(
            Icons.logout,
            color: Theme.of(context).colorScheme.error,
          ),
          title: Text(
            l10n.t('common.logout'),
            style: TextStyle(color: Theme.of(context).colorScheme.error),
          ),
          onTap: () => _confirmLogout(l10n),
        ),
        const Divider(height: 24),
        _SectionHeader(title: l10n.t('settings.about')),
        _ValueRow(
          icon: Icons.info_outline,
          title: l10n.t('settings.about'),
          value: appVersion,
          onTap: () => _showAbout(l10n),
        ),
      ],
    );
  }

  // ---------------------------------------------------------------------------
  // iOS (Cupertino)
  // ---------------------------------------------------------------------------

  Widget _buildCupertino(
    AppLocalizations l10n,
    SettingsState settings,
    SettingsController controller,
    bool registered,
    bool isAdmin,
    bool backupFailing,
  ) {
    final languageCode = settings.locale?.languageCode ?? 'system';
    return ListView(
      children: [
        CupertinoListSection.insetGrouped(
          header: Text(l10n.t('settings.appearance')),
          children: [
            CupertinoListTile.notched(
              leading: const Icon(CupertinoIcons.brightness),
              title: Text(l10n.t('settings.theme')),
              additionalInfo: Text(_themeLabel(l10n, settings.themeMode)),
              trailing: const CupertinoListTileChevron(),
              onTap: () => _showThemePicker(l10n, settings, controller),
            ),
            CupertinoListTile.notched(
              leading: const Icon(CupertinoIcons.globe),
              title: Text(l10n.t('settings.language')),
              additionalInfo: Text(_languageLabel(l10n, languageCode)),
              trailing: const CupertinoListTileChevron(),
              onTap: () => _showLanguagePicker(l10n, languageCode, controller),
            ),
          ],
        ),
        CupertinoListSection.insetGrouped(
          header: Text(l10n.t('settings.server')),
          footer: Text(l10n.t('settings.serverLocked')),
          children: [
            CupertinoListTile.notched(
              leading: const Icon(CupertinoIcons.globe),
              title: Text(l10n.t('settings.serverUrl')),
              additionalInfo: Text(_serverHost(settings.baseUrl)),
              trailing: const CupertinoListTileChevron(),
              onTap: () => showServerQr(context, ref),
            ),
          ],
        ),
        CupertinoListSection.insetGrouped(
          header: Text(l10n.t('settings.device')),
          children: [
            CupertinoListTile.notched(
              leading: Icon(
                registered
                    ? CupertinoIcons.bell_fill
                    : CupertinoIcons.bell_slash,
              ),
              title: Text(
                registered
                    ? l10n.t('settings.deviceRegistered')
                    : l10n.t('settings.deviceNotRegistered'),
              ),
            ),
            if (_privacy != null)
              for (final option in _privacyOptions)
                CupertinoListTile.notched(
                  leading: Icon(
                    option.$1 == _privacy
                        ? CupertinoIcons.checkmark_circle_fill
                        : CupertinoIcons.circle,
                  ),
                  title: Text(l10n.t(option.$2)),
                  subtitle: Text(l10n.t(option.$3)),
                  onTap: () => _setPrivacy(option.$1),
                ),
          ],
        ),
        CupertinoListSection.insetGrouped(
          header: Text(l10n.t('settings.account')),
          children: [
            CupertinoListTile.notched(
              leading: const Icon(CupertinoIcons.person),
              title: Text(l10n.t('settings.profile')),
              trailing: const CupertinoListTileChevron(),
              onTap: () => context.push('/profile'),
            ),
            CupertinoListTile.notched(
              leading: const Icon(CupertinoIcons.shield),
              title: Text(l10n.t('security.menuItem')),
              trailing: const CupertinoListTileChevron(),
              onTap: () => context.push('/security'),
            ),
            CupertinoListTile.notched(
              leading: Icon(
                CupertinoIcons.lock_shield,
                color: backupFailing ? CupertinoColors.destructiveRed : null,
              ),
              title: Text(l10n.t('recovery.menuItem')),
              subtitle: backupFailing
                  ? Text(
                      l10n.t('recovery.backupFailing'),
                      style: const TextStyle(
                        color: CupertinoColors.destructiveRed,
                      ),
                    )
                  : null,
              trailing: const CupertinoListTileChevron(),
              onTap: () => showRecoveryCodeSheet(context, ref),
            ),
            if (isAdmin)
              CupertinoListTile.notched(
                leading: const Icon(CupertinoIcons.shield_lefthalf_fill),
                title: Text(l10n.t('admin.title')),
                trailing: const CupertinoListTileChevron(),
                onTap: () => context.push('/admin'),
              ),
            CupertinoListTile.notched(
              leading: const Icon(
                CupertinoIcons.square_arrow_right,
                color: CupertinoColors.destructiveRed,
              ),
              title: Text(
                l10n.t('common.logout'),
                style: const TextStyle(color: CupertinoColors.destructiveRed),
              ),
              onTap: () => _confirmLogout(l10n),
            ),
          ],
        ),
        CupertinoListSection.insetGrouped(
          header: Text(l10n.t('settings.about')),
          children: [
            CupertinoListTile.notched(
              leading: const Icon(CupertinoIcons.info),
              title: Text(l10n.t('settings.about')),
              additionalInfo: Text(appVersion),
              trailing: const CupertinoListTileChevron(),
              onTap: () => _showAbout(l10n),
            ),
          ],
        ),
      ],
    );
  }

  // ---------------------------------------------------------------------------
  // Choosers (Material). The iOS tree has its own pickers; these are the equivalents.
  // ---------------------------------------------------------------------------

  String _privacyLabel(AppLocalizations l10n, String value) {
    for (final option in _privacyOptions) {
      if (option.$1 == value) return l10n.t(option.$2);
    }
    return value;
  }

  /// One chooser for every setting that picks from a fixed set.
  ///
  /// Written once rather than per setting: three near-identical dialogs is three places for the
  /// selected-state rendering to drift, and this screen has already had two idioms living side by
  /// side for exactly that reason.
  Future<T?> _choose<T>({
    required String title,
    required List<(T, String, String?)> options,
    required T current,
  }) {
    return showGlassDialog<T>(
      context: context,
      builder: (context) => GlassDialog(
        title: Text(title),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (final option in options)
              // A tapped row with a check rather than RadioListTile: the Radio group API wants a
              // RadioGroup ancestor now, and this matches the rows the rest of the screen uses.
              _ChoiceRow(
                label: option.$2,
                detail: option.$3,
                selected: option.$1 == current,
                onTap: () => Navigator.of(context).pop(option.$1),
              ),
          ],
        ),
      ),
    );
  }

  Future<void> _pickTheme(
    AppLocalizations l10n,
    SettingsState settings,
    SettingsController controller,
  ) async {
    final picked = await _choose<ThemeMode>(
      title: l10n.t('settings.theme'),
      current: settings.themeMode,
      options: [
        (ThemeMode.system, l10n.t('settings.themeSystem'), null),
        (ThemeMode.light, l10n.t('settings.themeLight'), null),
        (ThemeMode.dark, l10n.t('settings.themeDark'), null),
      ],
    );
    if (picked != null) controller.setThemeMode(picked);
  }

  Future<void> _pickLanguage(
    AppLocalizations l10n,
    SettingsState settings,
    SettingsController controller,
  ) async {
    final picked = await _choose<String>(
      title: l10n.t('settings.language'),
      current: settings.locale?.languageCode ?? 'system',
      options: [
        ('system', l10n.t('settings.languageSystem'), null),
        ('en', l10n.t('settings.languageEn'), null),
        ('ru', l10n.t('settings.languageRu'), null),
      ],
    );
    if (picked == null) return;
    controller.setLocale(picked == 'system' ? null : Locale(picked));
  }

  Future<void> _pickPrivacy(AppLocalizations l10n) async {
    final picked = await _choose<String>(
      title: l10n.t('settings.aboutLockScreen'),
      current: _privacy ?? 'sender',
      options: [
        for (final option in _privacyOptions)
          (option.$1, l10n.t(option.$2), l10n.t(option.$3)),
      ],
    );
    if (picked != null) await _setPrivacy(picked);
  }

  /// Logging out is not destructive — the account and its history survive — but it is a door that
  /// shuts: the next screen is the sign-in form, and getting back in means having the password to
  /// hand. It was a single tap on a row in a list of rows.
  Future<void> _confirmLogout(AppLocalizations l10n) async {
    final confirmed = await showAdaptiveConfirm(
      context,
      title: l10n.t('common.logout'),
      message: l10n.t('common.logoutConfirm'),
      confirmLabel: l10n.t('common.logout'),
      cancelLabel: l10n.t('common.cancel'),
      isDestructive: true,
    );
    if (!confirmed || !mounted) return;
    await ref.read(authControllerProvider.notifier).logout();
  }

  Future<void> _showAbout(AppLocalizations l10n) {
    return showGlassDialog<void>(
      context: context,
      builder: (context) => GlassDialog(
        title: Text(l10n.t('settings.about')),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('${l10n.t('settings.aboutVersion')} $appVersion'),
            const SizedBox(height: 8),
            Text(appWebsite),
            const SizedBox(height: 8),
            Text(appCopyright),
          ],
        ),
        actions: [
          GlassDialogAction(
            label: l10n.t('common.close'),
            emphasised: true,
            onPressed: () => Navigator.of(context).pop(),
          ),
        ],
      ),
    );
  }

  String _themeLabel(AppLocalizations l10n, ThemeMode mode) {
    return switch (mode) {
      ThemeMode.system => l10n.t('settings.themeSystem'),
      ThemeMode.light => l10n.t('settings.themeLight'),
      ThemeMode.dark => l10n.t('settings.themeDark'),
    };
  }

  String _languageLabel(AppLocalizations l10n, String code) {
    return switch (code) {
      'en' => l10n.t('settings.languageEn'),
      'ru' => l10n.t('settings.languageRu'),
      _ => l10n.t('settings.languageSystem'),
    };
  }

  Future<void> _showThemePicker(
    AppLocalizations l10n,
    SettingsState settings,
    SettingsController controller,
  ) async {
    await showCupertinoModalPopup<void>(
      context: context,
      builder: (sheetContext) => CupertinoActionSheet(
        title: Text(l10n.t('settings.theme')),
        actions: [
          for (final mode in ThemeMode.values)
            CupertinoActionSheetAction(
              onPressed: () {
                controller.setThemeMode(mode);
                Navigator.pop(sheetContext);
              },
              isDefaultAction: settings.themeMode == mode,
              child: Text(_themeLabel(l10n, mode)),
            ),
        ],
        cancelButton: CupertinoActionSheetAction(
          onPressed: () => Navigator.pop(sheetContext),
          child: Text(l10n.t('common.cancel')),
        ),
      ),
    );
  }

  Future<void> _showLanguagePicker(
    AppLocalizations l10n,
    String currentCode,
    SettingsController controller,
  ) async {
    const codes = ['system', 'en', 'ru'];
    await showCupertinoModalPopup<void>(
      context: context,
      builder: (sheetContext) => CupertinoActionSheet(
        title: Text(l10n.t('settings.language')),
        actions: [
          for (final code in codes)
            CupertinoActionSheetAction(
              onPressed: () {
                controller.setLocale(code == 'system' ? null : Locale(code));
                Navigator.pop(sheetContext);
              },
              isDefaultAction: currentCode == code,
              child: Text(_languageLabel(l10n, code)),
            ),
        ],
        cancelButton: CupertinoActionSheetAction(
          onPressed: () => Navigator.pop(sheetContext),
          child: Text(l10n.t('common.cancel')),
        ),
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.title});

  final String title;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
      child: Text(
        title.toUpperCase(),
        style: TextStyle(
          fontSize: 12,
          fontWeight: FontWeight.w700,
          letterSpacing: 0.6,
          color: Theme.of(context).colorScheme.primary,
        ),
      ),
    );
  }
}

/// A settings row: what it is on the left, what it is set to on the right, a chooser on tap.
class _ValueRow extends StatelessWidget {
  const _ValueRow({
    required this.icon,
    required this.title,
    required this.value,
    required this.onTap,
  });

  final IconData icon;
  final String title;
  final String value;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return ListTile(
      leading: Icon(icon),
      title: Text(title),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          // Bounded, because a server URL is longer than the space for it and an unbounded Text in
          // a trailing Row throws rather than eliding.
          ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 180),
            child: Text(
              value,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              textAlign: TextAlign.end,
              style: theme.textTheme.bodyMedium?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          const Icon(Icons.chevron_right),
        ],
      ),
      onTap: onTap,
    );
  }
}

/// One option in a [_choose] dialog: a label, an optional line of detail, and a check when it is
/// the one currently in force.
class _ChoiceRow extends StatelessWidget {
  const _ChoiceRow({
    required this.label,
    required this.detail,
    required this.selected,
    required this.onTap,
  });

  final String label;
  final String? detail;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 11),
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    label,
                    style: TextStyle(
                      fontSize: 16,
                      fontWeight: selected ? FontWeight.w600 : FontWeight.w400,
                      color: scheme.onSurface,
                    ),
                  ),
                  if (detail != null) ...[
                    const SizedBox(height: 2),
                    Text(
                      detail!,
                      style: TextStyle(
                        fontSize: 13,
                        color: scheme.onSurfaceVariant,
                      ),
                    ),
                  ],
                ],
              ),
            ),
            // Only the chosen one is marked. A column of empty circles is a column of controls that
            // all look available to press; the question is which one is set, not which exist.
            if (selected) ...[
              const SizedBox(width: 12),
              Icon(Icons.check, size: 20, color: scheme.primary),
            ],
          ],
        ),
      ),
    );
  }
}

/// The host, without the unlisted path prefix.
///
/// The prefix is twelve meaningless characters and takes more room than the name of the server it
/// belongs to; nobody verifies it by eye. It is in the QR and on the clipboard, which is where it
/// gets used.
String _serverHost(String baseUrl) {
  final host = Uri.tryParse(baseUrl)?.host ?? '';
  return host.isEmpty ? baseUrl : host;
}

/// Why the address cannot be changed from here.
///
/// It used to be editable, with its own Save button — which meant an account could be signed in
/// against one server while the app was told to talk to another. Everything below the change
/// (the session, the device registration, the MLS identity, the keys) belongs to the server it was
/// made on, and none of it survives being pointed elsewhere. Signing out is what actually ends
/// those, so signing out is what changes the server.
class _ServerNote extends StatelessWidget {
  const _ServerNote();

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 4),
      child: Text(
        AppLocalizations.of(context).t('settings.serverLocked'),
        style: TextStyle(fontSize: 12, color: scheme.onSurfaceVariant),
      ),
    );
  }
}
