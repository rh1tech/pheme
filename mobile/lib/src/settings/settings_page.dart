import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

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

  /// Whether notifications on this account may name the sender.
  ///
  /// It lives here and not in [SettingsState] because it is a property of the ACCOUNT, not
  /// of this handset: it has to hold on every device the user signs in on, and SettingsState
  /// is device-local secure storage that never reaches the server. Null while loading, and
  /// if the load fails — the switch stays hidden rather than showing a default that might be
  /// the opposite of the truth and silently rewriting it on the first tap.
  bool? _showSender;

  @override
  void initState() {
    super.initState();
    _loadShowSender();
  }

  Future<void> _loadShowSender() async {
    try {
      final me = await ref.read(repositoryProvider).getMe();
      if (mounted) setState(() => _showSender = me.showsSender);
    } on Object {
      // Leave it null: better no switch than one showing a state we could not confirm.
    }
  }

  Future<void> _setShowSender(bool value) async {
    final previous = _showSender;
    // Optimistic: a privacy switch that lags behind the finger feels broken. Rolled back below
    // if the server refuses, so it can never end up showing something the account does not say.
    setState(() => _showSender = value);
    try {
      await ref
          .read(repositoryProvider)
          .updateMe(notificationPrivacy: value ? 'sender' : 'generic');
    } on Object {
      if (!mounted) return;
      setState(() => _showSender = previous);
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
        ListTile(
          leading: const Icon(Icons.brightness_6_outlined),
          title: Text(l10n.t('settings.theme')),
          trailing: DropdownButton<ThemeMode>(
            value: settings.themeMode,
            underline: const SizedBox.shrink(),
            onChanged: (mode) {
              if (mode != null) controller.setThemeMode(mode);
            },
            items: [
              DropdownMenuItem(
                value: ThemeMode.system,
                child: Text(l10n.t('settings.themeSystem')),
              ),
              DropdownMenuItem(
                value: ThemeMode.light,
                child: Text(l10n.t('settings.themeLight')),
              ),
              DropdownMenuItem(
                value: ThemeMode.dark,
                child: Text(l10n.t('settings.themeDark')),
              ),
            ],
          ),
        ),
        ListTile(
          leading: const Icon(Icons.translate_outlined),
          title: Text(l10n.t('settings.language')),
          trailing: DropdownButton<String>(
            value: settings.locale?.languageCode ?? 'system',
            underline: const SizedBox.shrink(),
            onChanged: (code) {
              controller.setLocale(
                code == null || code == 'system' ? null : Locale(code),
              );
            },
            items: [
              DropdownMenuItem(
                value: 'system',
                child: Text(l10n.t('settings.languageSystem')),
              ),
              DropdownMenuItem(
                value: 'en',
                child: Text(l10n.t('settings.languageEn')),
              ),
              DropdownMenuItem(
                value: 'ru',
                child: Text(l10n.t('settings.languageRu')),
              ),
            ],
          ),
        ),
        const Divider(height: 24),
        _SectionHeader(title: l10n.t('settings.server')),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
          child: Row(
            children: [
              Expanded(
                child: AdaptiveTextField(
                  controller: _baseUrl,
                  label: l10n.t('settings.serverUrl'),
                  keyboardType: TextInputType.url,
                  onSubmitted: (_) => _saveBaseUrl(),
                ),
              ),
              const SizedBox(width: 8),
              AdaptiveButton.filled(
                onPressed: _saveBaseUrl,
                child: Text(l10n.t('common.save')),
              ),
            ],
          ),
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
        if (_showSender != null)
          SwitchListTile.adaptive(
            secondary: const Icon(Icons.visibility_outlined),
            title: Text(l10n.t('settings.showSender')),
            subtitle: Text(l10n.t('settings.showSenderHint')),
            value: _showSender!,
            onChanged: _setShowSender,
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
                  AdaptiveButton.filled(
                    onPressed: _saveBaseUrl,
                    child: Text(l10n.t('common.save')),
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
            if (_showSender != null)
              CupertinoListTile.notched(
                leading: const Icon(CupertinoIcons.eye),
                title: Text(l10n.t('settings.showSender')),
                subtitle: Text(l10n.t('settings.showSenderHint')),
                trailing: CupertinoSwitch(
                  value: _showSender!,
                  onChanged: _setShowSender,
                ),
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
      ],
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
