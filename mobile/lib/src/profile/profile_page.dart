import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';

import '../core/api_exception.dart';
import '../core/providers.dart';
import '../core/snackbar.dart';
import '../l10n/app_localizations.dart';
import '../models/models.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/error_view.dart';
import '../widgets/photo_source_sheet.dart';

/// Keep in sync with the server avatar limit (api/internal/channel/notify_input.go).
const _maxImageBytes = 10 * 1024 * 1024;

/// The signed-in user's profile: avatar, unique username, and contact fields.
class ProfilePage extends ConsumerStatefulWidget {
  const ProfilePage({super.key});

  @override
  ConsumerState<ProfilePage> createState() => _ProfilePageState();
}

class _ProfilePageState extends ConsumerState<ProfilePage> {
  final _username = TextEditingController();
  final _displayName = TextEditingController();
  final _bio = TextEditingController();
  final _phone = TextEditingController();
  final _website = TextEditingController();
  final _picker = ImagePicker();

  User? _user;
  bool _loading = true;
  bool _error = false;
  bool _saving = false;
  bool _uploading = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _username.dispose();
    _displayName.dispose();
    _bio.dispose();
    _phone.dispose();
    _website.dispose();
    super.dispose();
  }

  void _fill(User u) {
    _user = u;
    _username.text = u.username ?? '';
    _displayName.text = u.displayName ?? '';
    _bio.text = u.bio ?? '';
    _phone.text = u.phone ?? '';
    _website.text = u.website ?? '';
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = false;
    });
    try {
      final u = await ref.read(repositoryProvider).getMe();
      if (!mounted) return;
      setState(() {
        _fill(u);
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = true;
      });
    }
  }

  Future<void> _save() async {
    FocusScope.of(context).unfocus();
    setState(() => _saving = true);
    final l10n = context.l10n;
    try {
      final updated = await ref
          .read(repositoryProvider)
          .updateMe(
            username: _username.text.trim(),
            displayName: _displayName.text.trim(),
            bio: _bio.text.trim(),
            phone: _phone.text.trim(),
            website: _website.text.trim(),
          );
      if (!mounted) return;
      setState(() => _fill(updated));
      notifySuccess(context, l10n.t('profile.saved'));
    } on ApiException catch (e) {
      if (!mounted) return;
      notifyError(
        context,
        e.statusCode == 409
            ? l10n.t('profile.usernameTaken')
            : l10n.t('profile.saveFailed'),
        e,
      );
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('profile.saveFailed'), e);
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  Future<void> _pickAvatar() async {
    final l10n = context.l10n;
    final source = await askPhotoSource(context);
    if (source == null || !mounted) return;
    final picked = await _picker.pickImage(source: source);
    if (picked == null || !mounted) return;
    if (await picked.length() > _maxImageBytes) {
      if (mounted) {
        notifyError(
          context,
          l10n.tp('channel.imageTooLarge', {'name': picked.name}),
        );
      }
      return;
    }
    if (!mounted) return;
    setState(() => _uploading = true);
    try {
      final updated = await ref
          .read(repositoryProvider)
          .uploadAvatar(picked.path);
      if (!mounted) return;
      setState(() => _fill(updated));
      notifySuccess(context, l10n.t('profile.avatarUpdated'));
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('profile.avatarFailed'), e);
    } finally {
      if (mounted) setState(() => _uploading = false);
    }
  }

  Future<void> _removeAvatar() async {
    final l10n = context.l10n;
    setState(() => _uploading = true);
    try {
      final updated = await ref.read(repositoryProvider).deleteAvatar();
      if (!mounted) return;
      setState(() => _fill(updated));
      notifySuccess(context, l10n.t('profile.avatarRemoved'));
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('profile.avatarFailed'), e);
    } finally {
      if (mounted) setState(() => _uploading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return AdaptiveScaffold(
      title: Text(l10n.t('profile.title')),
      body: _body(context, l10n),
    );
  }

  Widget _body(BuildContext context, AppLocalizations l10n) {
    if (_loading) return const Center(child: AdaptiveProgress());
    final user = _user;
    if (_error || user == null) {
      return ErrorView(message: l10n.t('profile.loadFailed'), onRetry: _load);
    }
    final scheme = Theme.of(context).colorScheme;
    final avatarId = user.avatarId;
    final avatarUrl = (avatarId != null && avatarId.isNotEmpty)
        ? ref.read(repositoryProvider).imageUrl(avatarId)
        : null;
    final initials = (user.displayName?.isNotEmpty ?? false)
        ? user.displayName!
        : (user.username?.isNotEmpty ?? false)
        ? user.username!
        : user.email;

    return ListView(
      // Wider gutters and a bottom that clears the home indicator. At 16 all round the fields ran
      // almost to the edge of the screen, which on a form of six stacked boxes reads as a wall
      // rather than as a set of things to fill in.
      padding: const EdgeInsets.fromLTRB(20, 20, 20, 40),
      children: [
        Center(
          child: Column(
            children: [
              CircleAvatar(
                radius: 44,
                backgroundColor: scheme.primaryContainer,
                foregroundImage: avatarUrl != null
                    ? NetworkImage(avatarUrl)
                    : null,
                child: Text(
                  initials.characters.take(2).toString().toUpperCase(),
                  style: const TextStyle(fontSize: 24),
                ),
              ),
              const SizedBox(height: 8),
              Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  AdaptiveButton.text(
                    onPressed: _uploading ? null : _pickAvatar,
                    child: Text(l10n.t('profile.uploadAvatar')),
                  ),
                  if (avatarId != null && avatarId.isNotEmpty)
                    AdaptiveButton.text(
                      onPressed: _uploading ? null : _removeAvatar,
                      child: Text(l10n.t('profile.removeAvatar')),
                    ),
                ],
              ),
              // Under the avatar, centred, rather than alone in the top-left corner.
              //
              // It is a caption for the whole screen — "how you appear to others" — and at the top
              // it read as a stray line of body text that the page had failed to lay out, sitting
              // above a centred avatar it had no visible relationship to. Beneath the thing it is
              // actually describing, it reads as a caption.
              const SizedBox(height: 4),
              Text(
                l10n.t('profile.subtitle'),
                textAlign: TextAlign.center,
                style: TextStyle(fontSize: 13, color: scheme.onSurfaceVariant),
              ),
            ],
          ),
        ),
        const SizedBox(height: 28),
        AdaptiveTextField(
          controller: _username,
          label: l10n.t('profile.username'),
          textInputAction: TextInputAction.next,
        ),
        const SizedBox(height: 6),
        Padding(
          padding: const EdgeInsets.only(left: 4),
          child: Text(
            l10n.t('profile.usernameHint'),
            style: TextStyle(fontSize: 12, color: scheme.onSurfaceVariant),
          ),
        ),
        const SizedBox(height: 16),
        AdaptiveTextField(
          controller: _displayName,
          label: l10n.t('profile.displayName'),
          textInputAction: TextInputAction.next,
        ),
        const SizedBox(height: 28),
        Text(
          l10n.t('profile.contactInfo'),
          style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 15),
        ),
        const SizedBox(height: 14),
        AdaptiveTextField(
          controller: _bio,
          label: l10n.t('profile.bio'),
          minLines: 2,
          maxLines: 4,
        ),
        const SizedBox(height: 16),
        AdaptiveTextField(
          controller: _phone,
          label: l10n.t('profile.phone'),
          keyboardType: TextInputType.phone,
          textInputAction: TextInputAction.next,
        ),
        const SizedBox(height: 16),
        AdaptiveTextField(
          controller: _website,
          label: l10n.t('profile.website'),
          keyboardType: TextInputType.url,
        ),
        const SizedBox(height: 28),
        AdaptiveButton.filled(
          onPressed: _saving ? null : _save,
          child: _saving
              ? const AdaptiveProgress(size: 18)
              : Text(l10n.t('profile.save')),
        ),
      ],
    );
  }
}
