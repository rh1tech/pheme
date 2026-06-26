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
