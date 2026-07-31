// What the shield on a chat is actually claiming, and the colour that claims it.
//
// The lock in the chat bar used to say one thing — "this conversation is encrypted" — which is
// true of every conversation in the app and therefore tells nobody anything. The question a person
// actually has is narrower and much more useful: *if I lost this phone right now, would I lose
// what I just wrote?*
//
// That question has a real answer, and until recently the answer was often "yes" while nothing on
// screen said so. A message body exists on exactly one device between the moment it is written and
// the moment a backup carries it off — MLS destroys the message key on encrypt and on decrypt — so
// a device with no recovery code, or one whose backups have been failing for a week, is one
// accident away from losing history that nothing can reconstruct. None of that was visible.
//
// So the shield now carries a status tint, and the tint is about the two things that can go wrong:
// whether the history is recoverable, and whether the person on the other end is who they claim.

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../crypto/mls_service.dart';
import 'chat_providers.dart';
import 'safety_pin_store.dart';

/// How much of the shield's tint reaches the eye.
///
/// Deliberately faint. This is ambient reassurance, not an alert: a control that shouts at
/// somebody every time they open a chat gets ignored within a day, and then it is worth nothing on
/// the day it matters. Red is allowed to be a little stronger than the rest, because it is the only
/// one that means "act".
const _calmOpacity = 0.20;
const _urgentOpacity = 0.28;

enum ShieldLevel {
  /// Everything written here is off the device, and the other end is verified. Nothing to do.
  secure,

  /// Working, but not finished — bodies still waiting to reach the server, or an unverified
  /// contact. Worth noticing, not worth interrupting anybody over.
  attention,

  /// The history is not protected: no recovery code at all, or backups that are failing. This is
  /// the state where losing the handset loses messages permanently.
  atRisk,
}

/// The shield's verdict for one conversation.
@immutable
class ShieldStatus {
  const ShieldStatus({
    required this.level,
    required this.backup,
    required this.verified,
  });

  final ShieldLevel level;
  final BackupHealth backup;

  /// Whether this conversation's safety number has been confirmed in person. Null while it is
  /// still being read, so the shield does not flash a warning it may be about to withdraw.
  final bool? verified;

  /// The tint painted behind the shield. Null for [ShieldLevel.secure] on purpose — a green wash
  /// on every chat all the time is decoration, and decoration is what the eye stops seeing.
  Color? tint(ColorScheme scheme) => switch (level) {
    ShieldLevel.secure => null,
    ShieldLevel.attention => scheme.tertiary.withValues(alpha: _calmOpacity),
    ShieldLevel.atRisk => scheme.error.withValues(alpha: _urgentOpacity),
  };
}

/// The shield's state for a conversation: backup health, plus whether this contact is verified.
final shieldStatusProvider = Provider.family<ShieldStatus, String>((
  ref,
  conversationId,
) {
  final backup = ref.watch(backupHealthProvider);
  final verified = ref.watch(conversationVerifiedProvider(conversationId));

  return ShieldStatus(
    level: shieldLevelFor(backup, verified.value),
    backup: backup,
    verified: verified.value,
  );
});

/// The whole judgement, as a plain function.
///
/// Pulled out of the provider so the rules can be tested by CALLING them rather than by restating
/// them in a test — a test that re-implements the decision it is checking passes whether or not
/// the app agrees with it, which is how a green suite ends up sitting on top of a broken feature.
ShieldLevel shieldLevelFor(BackupHealth backup, bool? verified) {
  // Ordered by severity, because a device with no backup at all does not become less urgent for
  // also having an unverified contact.
  return switch (backup) {
    // Nothing is being backed up, or the attempts are failing. Either way the history on this
    // device is the only copy there is.
    BackupHealth(armed: false) => ShieldLevel.atRisk,
    BackupHealth(failing: true) => ShieldLevel.atRisk,
    // Bodies sealed but not yet delivered — normally a fraction of a second, so this showing at
    // all means something is slow or offline.
    BackupHealth(pending: > 0) => ShieldLevel.attention,
    _ when verified == false => ShieldLevel.attention,
    _ => ShieldLevel.secure,
  };
}

/// This device's backup health, re-read on a timer.
///
/// Polled rather than pushed because the underlying state is a plain field on MlsService that
/// several unrelated paths write — a send, a decrypt, a failed append — and threading a
/// notification through all of them to move a tint is more machinery than the tint is worth.
final backupHealthProvider = StreamProvider.autoDispose<BackupHealth>((ref) {
  final mls = ref.watch(mlsServiceProvider);
  // The CURRENT value first, then the poll. A periodic stream emits nothing until its first tick,
  // and the fallback for "nothing yet" is the dormant reading — so for the first two seconds after
  // any screen opened, a perfectly healthy device reported that it had no recovery code and the
  // "back up now" button sat there disabled. An indicator whose first answer is wrong is worse
  // than one that is slow: people read it immediately and believe it.
  return Stream<BackupHealth>.multi((controller) {
    controller.add(mls.backupHealth);
    final timer = Timer.periodic(
      const Duration(seconds: 2),
      (_) => controller.add(mls.backupHealth),
    );
    controller.onCancel = timer.cancel;
  }).distinct(
    (a, b) =>
        a.armed == b.armed &&
        a.pending == b.pending &&
        a.failing == b.failing &&
        a.lastSucceededAt == b.lastSucceededAt,
  );
}).select((async) => async.value ?? _dormant);

const _dormant = BackupHealth(
  lastSucceededAt: null,
  lastError: null,
  armed: false,
);

/// Whether this conversation's safety number has been confirmed.
final conversationVerifiedProvider = FutureProvider.family<bool, String>((
  ref,
  conversationId,
) async {
  final pinned = await ref.watch(safetyPinStoreProvider).pinned(conversationId);
  return pinned != null;
});
