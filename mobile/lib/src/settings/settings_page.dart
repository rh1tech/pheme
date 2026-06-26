import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';
import '../core/snackbar.dart';
import '../l10n/app_localizations.dart';

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

    return Scaffold(
      appBar: AppBar(title: Text(l10n.t('settings.title'))),
      body: ListView(
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
                  child: TextField(
                    controller: _baseUrl,
                    keyboardType: TextInputType.url,
                    autocorrect: false,
                    textInputAction: TextInputAction.done,
                    onSubmitted: (_) => _saveBaseUrl(),
                    decoration: InputDecoration(
                      labelText: l10n.t('settings.serverUrl'),
                      hintText: l10n.t('settings.serverUrlHint'),
                      isDense: true,
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                FilledButton(
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
          const Divider(height: 24),
          _SectionHeader(title: l10n.t('settings.account')),
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
