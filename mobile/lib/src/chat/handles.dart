// A `username@host` handle names someone on another Pheme server. The host may be a
// full domain (`hub.example.com`) or a short nodelist alias (`kn86r`) — the
// server resolves either to a domain and then the username to an id. Local user search
// only ever returns people on this host, so a federated member is reached by typing
// their whole handle.
//
// Deliberately permissive: a handle that resolves to nobody just comes back as "user
// not found". This regex only decides whether to OFFER the remote add, not whether the
// person exists. Mirrors web/src/lib/handles.ts.
final RegExp _remoteHandle = RegExp(
  r'^[a-zA-Z0-9_.]{3,30}@[a-zA-Z0-9][a-zA-Z0-9.-]{1,}$',
);

/// Returns the trimmed handle if [input] looks like `username@host`, else null.
String? remoteHandle(String input) {
  final trimmed = input.trim();
  return _remoteHandle.hasMatch(trimmed) ? trimmed : null;
}
