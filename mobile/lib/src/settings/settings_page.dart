import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../channels/qr_scanner_page.dart';
import '../core/app_config.dart';
import '../core/providers.dart';
import '../core/snackbar.dart';
import '../crypto/recovery_gate.dart';
import '../l10n/app_localizations.dart';
import '../widgets/adaptive/adaptive.dart';
import 'settings_controller.dart';

/// App preferences: theme, language, API server, device push status and logout.
class SettingsPage extends ConsumerStatefulWidget {
  const SettingsPage({super.key});

  @override
  ConsumerState<SettingsPage> createState() => _SettingsPageState();
}

class _SettingsPageState extends ConsumerState<SettingsPage> {
  late final TextEditingController _baseUrl = TextEditingController(
    text: ref.read(settingsControllerProvider).baseUrl,
  );

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
    _baseUrl.dispose();
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
  Future<void> _scanBaseUrl() async {
    final l10n = context.l10n;
    final scanned = await Navigator.of(context).push<String>(
      MaterialPageRoute(
        fullscreenDialog: true,
        builder: (_) => QrScannerPage(
          raw: true,
          instruction: l10n.t('settings.serverScanHint'),
        ),
      ),
    );
    if (scanned == null || scanned.isEmpty || !mounted) return;
    _baseUrl.text = scanned;
    // Validation lives in _saveBaseUrl, so a QR carrying something that is not a
    // server URL is refused the same way a typo is.
    await _saveBaseUrl();
  }

  Future<void> _saveBaseUrl() async {
    final l10n = context.l10n;
    final value = _baseUrl.text.trim();
    final uri = Uri.tryParse(value);
    if (value.isEmpty ||
        uri == null ||
        !uri.hasScheme ||
        !(uri.isScheme('http') || uri.isScheme('https')) ||
        !uri.hasAuthority) {
      notifyError(context, l10n.t('settings.serverInvalid'));
      return;
    }
    FocusScope.of(context).unfocus();
    await ref.read(settingsControllerProvider.notifier).setBaseUrl(value);
    if (mounted) notifySuccess(context, l10n.t('settings.serverSaved'));
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final settings = ref.watch(settingsControllerProvider);
    final controller = ref.read(settingsControllerProvider.notifier);
    final registered = ref.watch(deviceControllerProvider) != null;

    return AdaptiveScaffold(
      grouped: isCupertino(context),
      title: Text(l10n.t('settings.title')),
      body: isCupertino(context)
          ? _buildCupertino(l10n, settings, controller, registered)
          : _buildMaterial(context, l10n, settings, controller, registered),
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
          value: _baseUrl.text,
          onTap: () => _editBaseUrl(l10n),
        ),
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
          leading: const Icon(Icons.vpn_key_outlined),
          title: Text(l10n.t('recovery.menuItem')),
          trailing: const Icon(Icons.chevron_right),
          onTap: () => showRecoveryCodeSheet(context, ref),
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
          onTap: () => ref.read(authControllerProvider.notifier).logout(),
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
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  AdaptiveTextField(
                    controller: _baseUrl,
                    label: l10n.t('settings.serverUrl'),
                    keyboardType: TextInputType.url,
                    onSubmitted: (_) => _saveBaseUrl(),
                  ),
                  const SizedBox(height: 12),
                  Row(
                    children: [
                      Expanded(
                        child: AdaptiveButton.filled(
                          onPressed: _saveBaseUrl,
                          child: Text(l10n.t('common.save')),
                        ),
                      ),
                      const SizedBox(width: 12),
                      AdaptiveButton(
                        onPressed: _scanBaseUrl,
                        child: Text(l10n.t('settings.serverScan')),
                      ),
                    ],
                  ),
                ],
              ),
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
              leading: const Icon(CupertinoIcons.lock_shield),
              title: Text(l10n.t('recovery.menuItem')),
              trailing: const CupertinoListTileChevron(),
              onTap: () => showRecoveryCodeSheet(context, ref),
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
              onTap: () => ref.read(authControllerProvider.notifier).logout(),
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
    return showDialog<T>(
      context: context,
      builder: (context) => SimpleDialog(
        title: Text(title),
        children: [
          for (final option in options)
            ListTile(
              // A tapped row with a check rather than RadioListTile: the Radio group API wants a
              // RadioGroup ancestor now, and this matches the rows the rest of the screen uses.
              leading: Icon(
                option.$1 == current
                    ? Icons.radio_button_checked
                    : Icons.radio_button_unchecked,
              ),
              title: Text(option.$2),
              subtitle: option.$3 == null ? null : Text(option.$3!),
              onTap: () => Navigator.of(context).pop(option.$1),
            ),
        ],
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

  /// The server address, in a dialog rather than a text field wired into the page.
  ///
  /// It sat inline with its own Save button, which is the one control on this screen that could be
  /// left half-edited: type a new address, never press Save, and the screen shows something that
  /// is not what the app is talking to.
  Future<void> _editBaseUrl(AppLocalizations l10n) async {
    final field = TextEditingController(text: _baseUrl.text);
    final saved = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(l10n.t('settings.serverUrl')),
        content: TextField(
          controller: field,
          keyboardType: TextInputType.url,
          autofocus: true,
          onSubmitted: (_) => Navigator.of(context).pop(true),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: Text(l10n.t('common.cancel')),
          ),
          TextButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: Text(l10n.t('common.save')),
          ),
        ],
      ),
    );
    if (saved ?? false) {
      _baseUrl.text = field.text.trim();
      await _saveBaseUrl();
    }
    field.dispose();
  }

  Future<void> _showAbout(AppLocalizations l10n) {
    return showDialog<void>(
      context: context,
      builder: (context) => AlertDialog(
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
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: Text(l10n.t('common.close')),
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
