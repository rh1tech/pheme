import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';
import '../core/snackbar.dart';
import '../l10n/app_localizations.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/glass/glass.dart';
import 'admin_models.dart';
import 'admin_providers.dart';
import 'widgets/admin_ui.dart';

/// The invitation link the app hands out.
///
/// A `pheme://` link rather than a web address, and it carries the server: whoever opens it on a
/// phone gets the app with the code and the address already filled in, which is the whole point of
/// generating an invitation from a phone in the first place. See core/deep_links.dart.
String inviteAppLink({required String code, required String server}) {
  final params = <String, String>{
    'code': code,
    if (server.isNotEmpty) 'server': server,
  };
  final query = params.entries
      .map(
        (e) =>
            '${Uri.encodeQueryComponent(e.key)}=${Uri.encodeQueryComponent(e.value)}',
      )
      .join('&');
  return 'pheme://invite?$query';
}

/// Invitations: who has been let in, who has been asked, and the one chance to copy a new link.
class AdminInvitesPage extends ConsumerWidget {
  const AdminInvitesPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = context.l10n;
    final admin = ref.read(adminRepositoryProvider);

    return AdminListScreen<AdminInvite>(
      title: l10n.t('admin.navInvites'),
      searchPlaceholder: l10n.t('admin.inviteSearch'),
      emptyLabel: l10n.t('admin.inviteNone'),
      fetch: (query, page) =>
          admin.listInvites(query: query, page: page, limit: adminPageLimit),
      primaryAction: (context, reload) => AdminAction(
        icon: Icons.add,
        iosIcon: CupertinoIcons.add,
        label: l10n.t('admin.inviteNew'),
        onPressed: () => _create(context, ref, reload),
      ),
      rowBuilder: (context, invite, reload) =>
          _InviteRow(invite: invite, onChanged: reload),
    );
  }

  Future<void> _create(
    BuildContext context,
    WidgetRef ref,
    VoidCallback reload,
  ) async {
    final l10n = context.l10n;
    final options = await showModalBottomSheet<({String note, int days})>(
      context: context,
      isScrollControlled: true,
      builder: (_) => const _CreateInviteSheet(),
    );
    if (options == null || !context.mounted) return;

    try {
      final created = await ref
          .read(adminRepositoryProvider)
          .createInvite(note: options.note, expiresInDays: options.days);
      if (!context.mounted) return;
      reload();
      // The link is shown IMMEDIATELY and only now. Nothing stored can reproduce it — the server
      // keeps a hash — so a dialog dismissed without copying has thrown the invitation away.
      final server = ref.read(settingsControllerProvider).baseUrl;
      await _showLink(context, code: created.code ?? '', server: server);
    } on Object catch (e) {
      if (context.mounted) {
        notifyError(context, l10n.t('admin.inviteCreateFailed'), e);
      }
    }
  }

  Future<void> _showLink(
    BuildContext context, {
    required String code,
    required String server,
  }) {
    final l10n = context.l10n;
    final link = inviteAppLink(code: code, server: server);
    return showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      // Not dismissible by tapping away: this is the only time the link exists, and a stray tap
      // outside a sheet is not a decision to discard an invitation.
      isDismissible: false,
      enableDrag: false,
      builder: (sheet) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(20),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                l10n.t('admin.inviteLink'),
                style: Theme.of(
                  context,
                ).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w600),
              ),
              const SizedBox(height: 8),
              Text(
                l10n.t('admin.inviteCopyOnce'),
                style: Theme.of(context).textTheme.bodySmall,
              ),
              const SizedBox(height: 16),
              SelectableText(
                link,
                style: Theme.of(context).textTheme.bodySmall?.copyWith(
                  fontFeatures: const [FontFeature.tabularFigures()],
                ),
              ),
              const SizedBox(height: 16),
              AdaptiveButton.filled(
                onPressed: () async {
                  await Clipboard.setData(ClipboardData(text: link));
                  if (!sheet.mounted) return;
                  Navigator.of(sheet).pop();
                  notifySuccess(context, l10n.t('admin.inviteCopied'));
                },
                child: Text(l10n.t('admin.inviteCopy')),
              ),
              const SizedBox(height: 8),
              AdaptiveButton.text(
                onPressed: () => Navigator.of(sheet).pop(),
                child: Text(l10n.t('common.close')),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _InviteRow extends ConsumerStatefulWidget {
  const _InviteRow({required this.invite, required this.onChanged});

  final AdminInvite invite;
  final VoidCallback onChanged;

  @override
  ConsumerState<_InviteRow> createState() => _InviteRowState();
}

class _InviteRowState extends ConsumerState<_InviteRow> {
  bool _busy = false;

  AdminInvite get _invite => widget.invite;

  Future<void> _revoke() async {
    final l10n = context.l10n;
    final ok = await showAdaptiveConfirm(
      context,
      title: l10n.t('admin.inviteRevoke'),
      message: l10n.t('admin.inviteRevokeConfirm'),
      confirmLabel: l10n.t('admin.inviteRevoke'),
      cancelLabel: l10n.t('common.cancel'),
      isDestructive: true,
    );
    if (!ok || !mounted) return;

    setState(() => _busy = true);
    try {
      await ref.read(adminRepositoryProvider).revokeInvite(_invite.id);
      if (!mounted) return;
      notifySuccess(context, l10n.t('admin.inviteRevoked'));
      widget.onChanged();
    } on Object catch (e) {
      if (mounted) notifyError(context, l10n.t('admin.inviteRevokeFailed'), e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  (String, Color?) _statusLabel(BuildContext context, AppLocalizations l10n) {
    final scheme = Theme.of(context).colorScheme;
    return switch (_invite.status) {
      InviteStatus.pending => (
        l10n.t('admin.inviteStatusPending'),
        scheme.primary,
      ),
      InviteStatus.used => (l10n.t('admin.inviteStatusUsed'), null),
      InviteStatus.revoked => (
        l10n.t('admin.inviteStatusRevoked'),
        scheme.error,
      ),
      InviteStatus.expired => (l10n.t('admin.inviteStatusExpired'), null),
    };
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final theme = Theme.of(context);
    final (statusLabel, statusColor) = _statusLabel(context, l10n);

    return ListTile(
      title: Text(
        _invite.note?.isNotEmpty == true ? _invite.note! : '${_invite.prefix}…',
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 4),
        child: Wrap(
          spacing: 6,
          runSpacing: 4,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            AdminBadge(label: statusLabel, color: statusColor),
            Text(
              '${_invite.prefix}…',
              style: theme.textTheme.bodySmall?.copyWith(
                fontFeatures: const [FontFeature.tabularFigures()],
              ),
            ),
            Text(
              adminDate(_invite.createdAt),
              style: theme.textTheme.bodySmall,
            ),
            if (_invite.expiresAt != null)
              Text(
                l10n
                    .t('admin.inviteExpiresOn')
                    .replaceAll('{date}', adminDate(_invite.expiresAt)),
                style: theme.textTheme.bodySmall,
              ),
          ],
        ),
      ),
      trailing: _busy
          ? const SizedBox.square(
              dimension: GlassMetrics.minTapTarget,
              child: Center(child: AdaptiveProgress(size: 18)),
            )
          // Only an unspent invitation can be withdrawn. Revoking a used one changes nothing —
          // the account it created is a separate matter, ended by blocking the user.
          : _invite.status != InviteStatus.pending
          ? null
          : GlassMenuButton(
              semanticLabel: l10n.t('admin.actions'),
              actions: [
                GlassMenuAction(
                  label: l10n.t('admin.inviteRevoke'),
                  icon: Icons.block,
                  destructive: true,
                  onSelected: _revoke,
                ),
              ],
            ),
    );
  }
}

/// Asks for the note and the expiry. Pops the pair, or null if cancelled.
class _CreateInviteSheet extends StatefulWidget {
  const _CreateInviteSheet();

  @override
  State<_CreateInviteSheet> createState() => _CreateInviteSheetState();
}

class _CreateInviteSheetState extends State<_CreateInviteSheet> {
  final _note = TextEditingController();

  /// Days until the invitation lapses; 0 means never. Offered as a few sensible spans rather than
  /// a number field: "how many days" is a question nobody has a considered answer to.
  static const _expiryOptions = <int>[0, 1, 7, 30, 90];
  int _days = 7;

  @override
  void dispose() {
    _note.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return SafeArea(
      child: Padding(
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
              l10n.t('admin.inviteNew'),
              style: Theme.of(
                context,
              ).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w600),
            ),
            const SizedBox(height: 16),
            AdaptiveTextField(
              controller: _note,
              autofocus: true,
              maxLength: 200,
              label: l10n.t('admin.inviteNote'),
              placeholder: l10n.t('admin.inviteNotePlaceholder'),
            ),
            const SizedBox(height: 12),
            Align(
              alignment: AlignmentDirectional.centerStart,
              child: Text(
                l10n.t('admin.inviteExpiry'),
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ),
            const SizedBox(height: 6),
            Wrap(
              spacing: 8,
              children: [
                for (final days in _expiryOptions)
                  ChoiceChip(
                    label: Text(
                      days == 0
                          ? l10n.t('admin.inviteNeverExpires')
                          : l10n
                                .t('admin.inviteDays')
                                .replaceAll('{count}', '$days'),
                    ),
                    selected: _days == days,
                    onSelected: (_) => setState(() => _days = days),
                  ),
              ],
            ),
            const SizedBox(height: 16),
            AdaptiveButton.filled(
              onPressed: () => Navigator.of(
                context,
              ).pop((note: _note.text.trim(), days: _days)),
              child: Text(l10n.t('admin.inviteCreate')),
            ),
          ],
        ),
      ),
    );
  }
}
