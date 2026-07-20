import 'dart:io' show Platform;

/// The User-Agent this app sends.
///
/// It exists to stop the app naming itself. Both native HTTP stacks build a
/// default from the bundle identity — Cronet sends
/// `tech.rh1.pheme.pheme_mobile/1 (…; Cronet/143…)`, CFNetwork sends
/// `Pheme/1 CFNetwork/… Darwin/…` — so adopting them without this would have
/// traded a TLS fingerprint for a plaintext header naming the application on
/// every single request. That is a worse trade than not adopting them at all.
///
/// The string must MATCH THE PLATFORM'S TLS STACK. A Chrome-on-Android
/// User-Agent arriving over an Apple TLS handshake is a contradiction, and
/// UA/TLS mismatch is a signal in its own right — one that bot-detection
/// systems key on explicitly. So: Chrome on Android, Safari on iOS.
///
/// The versions here are frozen and will drift out of date. That is a known and
/// accepted weakness: a slightly stale browser version is a far weaker signal
/// than an application name, and chasing exact version parity with whatever
/// Cronet or CFNetwork ships is a treadmill with no end. Refresh them when they
/// get embarrassing.
String phemeUserAgent() {
  if (Platform.isAndroid) {
    return 'Mozilla/5.0 (Linux; Android 15; K) AppleWebKit/537.36 '
        '(KHTML, like Gecko) Chrome/143.0.0.0 Mobile Safari/537.36';
  }
  if (Platform.isIOS) {
    return 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) '
        'AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Mobile/15E148 '
        'Safari/604.1';
  }
  if (Platform.isMacOS) {
    return 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) '
        'AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Safari/605.1.15';
  }
  // Desktop dev builds fall through to the Dart stack anyway, so there is no
  // platform fingerprint to be consistent with.
  return 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 '
      '(KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36';
}
