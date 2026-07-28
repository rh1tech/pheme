// Somebody else's profile, reached by tapping their avatar in a chat.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../chat/widgets/conversation_avatar.dart';
import '../core/api_exception.dart';
import '../core/providers.dart';
import '../l10n/app_localizations.dart';
import '../models/models.dart';
import '../widgets/adaptive/adaptive_controls.dart';
import '../widgets/adaptive/adaptive_scaffold.dart';
import '../widgets/error_view.dart';
import '../widgets/glass/glass.dart';

/// A read-only view of one person: their picture, their names, and whatever they wrote about
/// themselves.
///
/// [fallback] is what the caller already knows — the member row it was opened from — so the screen
/// paints a name and a face immediately rather than showing a spinner over information it is
/// holding. The request only ever adds to that.
class UserProfilePage extends ConsumerStatefulWidget {
  const UserProfilePage({super.key, required this.userId, this.fallback});

  final String userId;
  final PublicProfile? fallback;

  @override
  ConsumerState<UserProfilePage> createState() => _UserProfilePageState();
}

class _UserProfilePageState extends ConsumerState<UserProfilePage> {
  PublicProfile? _profile;
  bool _loading = true;

  /// Set only when there is nothing to show at all. A failure with a fallback in hand is not worth
  /// an error screen — the name and face the caller passed in are still true.
  bool _failed = false;

  @override
  void initState() {
    super.initState();
    _profile = widget.fallback;
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _failed = false;
    });
    try {
      final profile = await ref
          .read(repositoryProvider)
          .getUserProfile(widget.userId);
      if (!mounted) return;
      setState(() {
        _profile = profile;
        _loading = false;
      });
    } on ApiException catch (e) {
      if (!mounted) return;
      // A 404 covers both "no such person" and "this server is older than the endpoint". With
      // something already in hand, the right response to either is to show it and stop.
      setState(() {
        _loading = false;
        _failed = _profile == null && e.statusCode == 404;
      });
    } on Object {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _failed = _profile == null;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return AdaptiveScaffold(
      title: Text(l10n.t('profile.userTitle')),
      body: _body(context, l10n),
    );
  }

  Widget _body(BuildContext context, AppLocalizations l10n) {
    final profile = _profile;
    if (profile == null) {
      if (_loading) return const Center(child: AdaptiveProgress());
      if (_failed) {
        return ErrorView(
          message: l10n.t('profile.userNotFound'),
          onRetry: _load,
        );
      }
      return ErrorView(message: l10n.t('profile.loadFailed'), onRetry: _load);
    }

    final scheme = Theme.of(context).colorScheme;
    final avatarId = profile.avatarId;
    final name = profile.displayName?.isNotEmpty ?? false
        ? profile.displayName!
        : (profile.username?.isNotEmpty ?? false)
        ? profile.username!
        : l10n.t('profile.userTitle');

    return ListView(
      padding: const EdgeInsets.fromLTRB(20, 20, 20, 40),
      children: [
        Center(
          child: Column(
            children: [
              ConversationAvatar(
                id: profile.id,
                label: name,
                size: 96,
                imageUrl: (avatarId?.isNotEmpty ?? false)
                    ? ref.read(repositoryProvider).imageUrl(avatarId!)
                    : null,
              ),
              const SizedBox(height: 14),
              Text(
                name,
                textAlign: TextAlign.center,
                style: const TextStyle(
                  fontSize: 22,
                  fontWeight: FontWeight.w600,
                ),
              ),
              if (profile.username?.isNotEmpty ?? false) ...[
                const SizedBox(height: 4),
                Text(
                  '@${profile.username}',
                  style: TextStyle(
                    fontSize: 15,
                    color: scheme.onSurfaceVariant,
                  ),
                ),
              ],
            ],
          ),
        ),
        const SizedBox(height: 28),
        if (profile.bio?.isNotEmpty ?? false)
          _Field(label: l10n.t('profile.bio'), value: profile.bio!),
        if (profile.website?.isNotEmpty ?? false)
          _Field(label: l10n.t('profile.website'), value: profile.website!),
        // Nothing written, and nothing still on its way. Said plainly rather than left as a blank
        // half-screen, which reads as a screen that failed to load.
        if (!profile.hasDetails && !_loading)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(
              l10n.t('profile.nothingShared'),
              textAlign: TextAlign.center,
              style: TextStyle(fontSize: 14, color: scheme.onSurfaceVariant),
            ),
          ),
      ],
    );
  }
}

/// One published field: a quiet caption over the text, matching the labels on your own profile.
class _Field extends StatelessWidget {
  const _Field({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    return Padding(
      padding: const EdgeInsets.only(bottom: 18),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(left: 4, bottom: 6),
            child: Text(
              label,
              style: TextStyle(
                fontSize: 13,
                fontWeight: FontWeight.w500,
                color: scheme.onSurfaceVariant,
              ),
            ),
          ),
          Container(
            width: double.infinity,
            padding: const EdgeInsets.all(14),
            decoration: BoxDecoration(
              color: scheme.onSurface.withValues(alpha: 0.05),
              borderRadius: BorderRadius.circular(GlassMetrics.fieldRadius),
            ),
            child: SelectableText(
              value,
              style: const TextStyle(fontSize: 15, height: 1.35),
            ),
          ),
        ],
      ),
    );
  }
}
