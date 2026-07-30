// Links that open the app from outside it.
//
// ONE SCHEME, `pheme://`, and deliberately not https. An https App Link has to be verified against
// a domain the app ships the fingerprint of, and Pheme is self-hosted: the server an invitation
// points at is not known when the app is built, and there is no single host to verify. The
// alternative — an unverified https filter on a path like `/login` — would put Pheme in the "open
// with" chooser for every site on the internet that has a login page.
//
// So the admin panel renders both: a web link for a browser, and a `pheme://` link that carries the
// server address with it, for a phone.
//
//   pheme://invite?code=<code>&server=https://host.example/prefix
//   pheme://join?ref=ch_ab12cd34         (or a phetag, or ref@host)
//   pheme://server?url=https://host.example/prefix
//
// A link is DATA FROM OUTSIDE THE APP and is treated as such: everything below validates before it
// is believed, and an address that does not parse as a server address is dropped rather than saved.

import 'dart:async';

import 'package:app_links/app_links.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'validators.dart';

/// The custom scheme the app answers to. Registered in AndroidManifest.xml and Info.plist; the
/// three must agree, and there is no build-time check that they do.
const phemeScheme = 'pheme';

/// A link the app understands, already validated.
sealed class DeepLink {
  const DeepLink();
}

/// An invitation to create an account: the code, and optionally the server it is for.
final class InviteLink extends DeepLink {
  const InviteLink({required this.code, this.server});

  final String code;

  /// The server the invitation is for, normalised, or null if the link did not name one. Null is
  /// not an error: an invitation pasted without a server still works if the app already points at
  /// the right one, which it does for anybody re-registering on their usual host.
  final String? server;
}

/// A channel to join: a trigger id, a phetag, or `ref@host`.
final class JoinLink extends DeepLink {
  const JoinLink({required this.ref});

  final String ref;
}

/// An instruction to point this install at a different server. What the self-host kit's QR code
/// says, in link form.
final class ServerLink extends DeepLink {
  const ServerLink({required this.server});

  final String server;
}

/// Parses a URI into a link the app understands, or null if it is none of them.
///
/// Exported for its own sake: this is the whole of the trust boundary, and it is worth being able
/// to test it without an app, a platform channel or a running Android.
DeepLink? parseDeepLink(Uri uri) {
  if (uri.scheme != phemeScheme) return null;

  // `pheme://invite?...` parses with `invite` as the HOST, not as a path segment — there is no
  // authority in these links, so the first label lands there. Accepting the path form too costs
  // nothing and means a link written `pheme:///invite?...` by hand still works.
  final action = uri.host.isNotEmpty
      ? uri.host.toLowerCase()
      : (uri.pathSegments.isEmpty ? '' : uri.pathSegments.first.toLowerCase());

  switch (action) {
    case 'invite':
      final code = uri.queryParameters['code']?.trim() ?? '';
      if (code.isEmpty) return null;
      return InviteLink(
        code: code,
        server: _server(uri.queryParameters['server']),
      );
    case 'join':
      final ref = uri.queryParameters['ref']?.trim() ?? '';
      if (ref.isEmpty) return null;
      return JoinLink(ref: ref);
    case 'server':
      final server = _server(uri.queryParameters['url']);
      if (server == null) return null;
      return ServerLink(server: server);
    default:
      return null;
  }
}

/// Normalises a server address out of a link, or null when it is absent or not one.
///
/// A link that names a server is a link that can silently redirect an account somewhere else, so a
/// malformed one is DROPPED rather than passed through: the rest of the link is still useful, and
/// the app keeps pointing where it already did.
String? _server(String? raw) {
  final value = raw?.trim() ?? '';
  if (value.isEmpty) return null;
  return normalizeServerUrl(value);
}

/// The link waiting to be acted on, if any.
///
/// A link arrives whenever the platform feels like delivering it — before the first frame on a cold
/// start, mid-session on a warm one — and the screen that can act on it may not be built yet. So it
/// is PARKED here rather than dispatched, and whoever can handle it takes it and calls [consume].
/// A link that nothing claims is simply dropped the next time one arrives.
class DeepLinkController extends Notifier<DeepLink?> {
  StreamSubscription<Uri>? _sub;

  @override
  DeepLink? build() {
    ref.onDispose(() => _sub?.cancel());
    return null;
  }

  /// Starts listening. Safe to call more than once; only the first call subscribes.
  ///
  /// [links] is injectable so tests can drive it without a platform channel.
  Future<void> start({AppLinks? links}) async {
    if (_sub != null) return;
    final app = links ?? AppLinks();
    // uriLinkStream replays the initial link on the platforms that have one, so asking for it
    // separately would open the same invitation twice.
    _sub = app.uriLinkStream.listen(
      handle,
      // A malfunctioning platform channel must not take the app down with it: every link here is a
      // convenience, and the app works without any of them.
      onError: (Object e) => debugPrint('deep link stream error: $e'),
    );
  }

  /// Offers a URI to the app. Ignored unless it parses as something we act on.
  void handle(Uri uri) {
    final link = parseDeepLink(uri);
    if (link != null) state = link;
  }

  /// Takes the pending link, if it is of type [T], clearing it. Returns null otherwise.
  ///
  /// Typed because two screens are listening for different links at once — the login page wants an
  /// invitation, the channel list wants a join — and neither should swallow the other's.
  T? consume<T extends DeepLink>() {
    final link = state;
    if (link is! T) return null;
    state = null;
    return link;
  }
}

final deepLinkControllerProvider =
    NotifierProvider<DeepLinkController, DeepLink?>(DeepLinkController.new);
