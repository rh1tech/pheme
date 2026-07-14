/// The failures the encryption layer has that callers actually branch on.
///
/// Each of these is a distinct thing to tell the user, and none of them is a bug. Collapsing them
/// into one "encryption failed" would leave the UI unable to say the one thing that would help.
library;

/// This session's keys were destroyed or replaced — by a logout, or by a restore.
///
/// The in-memory client may still hold perfectly good private keys; the point is that it must not
/// use them, and must certainly not write them back over what replaced them.
class SessionInvalidatedException implements Exception {
  const SessionInvalidatedException();

  @override
  String toString() =>
      "this device's encryption keys were replaced or destroyed";
}

/// The person we are trying to reach has published no key packages on any device.
///
/// They have not opened Pheme anywhere that does encrypted chats, so there is nothing to build a
/// group with. Not an error on our side, and not permanent: they become reachable the moment they
/// do. The UI should say that, rather than showing a message that mysteriously fails to send.
class PeerKeysMissingException implements Exception {
  const PeerKeysMissingException();

  @override
  String toString() => 'that person has not set up encrypted chats yet';
}

/// This device holds no keys, but a backup is waiting on the server.
///
/// Minting a fresh identity here would be irreversible: the new client would publish key packages a
/// restore is about to throw away, and a peer claiming one would send a Welcome nobody has the
/// private half of — a message stuck forever. So we refuse, and make the user choose.
class NeedsRestoreException implements Exception {
  const NeedsRestoreException();

  @override
  String toString() => 'encrypted chats need to be restored on this device';
}

/// A restore was attempted onto a device that has already set up an identity.
///
/// Going ahead would replace it with an older snapshot and strand everything said in between.
class IdentityAlreadySetUpException implements Exception {
  const IdentityAlreadySetUpException();

  @override
  String toString() => 'this device already has encryption set up';
}

/// This device is not (yet) a member of the conversation's MLS group, so it cannot do the thing
/// being asked of it — add a member, remove one, place a call.
class NotInGroupException implements Exception {
  const NotInGroupException();

  @override
  String toString() =>
      "this device is not in the conversation's encrypted group yet";
}
