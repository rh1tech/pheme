/// Raised for non-2xx API responses, carrying the server-provided message.
class ApiException implements Exception {
  ApiException(this.statusCode, this.message);

  final int statusCode;
  final String message;

  @override
  String toString() => message;
}

/// Raised when the session is no longer valid and the user must re-authenticate.
class AuthException implements Exception {
  AuthException([this.message = 'session expired']);

  final String message;

  @override
  String toString() => message;
}

/// Raised when the server has no TURN configured, so calls cannot work at all.
///
/// Its own type rather than a 503 the caller has to recognise, because this is not a failure to
/// recover from: it is how the client learns not to offer a call button in the first place.
class CallingUnavailableException implements Exception {
  CallingUnavailableException([this.message = 'calling is not available']);

  final String message;

  @override
  String toString() => message;
}
