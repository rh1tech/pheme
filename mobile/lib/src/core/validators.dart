/// Client-side mirror of the server password policy
/// (api/internal/auth/password_policy.go). The server is the source of truth for
/// acceptance; these helpers drive the strength meter and disable submit early.
library;

const int minPasswordLength = 8;

const Set<String> _commonPasswords = {
  'password',
  'password1',
  'password123',
  'passw0rd',
  '12345678',
  '123456789',
  '1234567890',
  'qwerty123',
  'qwertyuiop',
  '1q2w3e4r',
  '1qaz2wsx',
  'iloveyou',
  'admin123',
  'welcome1',
  'welcome123',
  'letmein1',
  'abc12345',
  'football',
  'baseball',
  'sunshine',
  'princess',
  'trustno1',
  'starwars',
  'whatever',
  'changeme',
  'superman',
  '11111111',
  '00000000',
};

int _characterClasses(String pw) {
  var n = 0;
  if (RegExp(r'[a-z]').hasMatch(pw)) n++;
  if (RegExp(r'[A-Z]').hasMatch(pw)) n++;
  if (RegExp(r'[0-9]').hasMatch(pw)) n++;
  if (RegExp(r'[^a-zA-Z0-9]').hasMatch(pw)) n++;
  return n;
}

/// Whether [pw] satisfies the minimum policy: length, two character classes,
/// and not a well-known weak password.
bool isPasswordAcceptable(String pw) {
  return pw.length >= minPasswordLength &&
      _characterClasses(pw) >= 2 &&
      !_commonPasswords.contains(pw.toLowerCase());
}

/// A 0–4 strength score for the meter.
int passwordScore(String pw) {
  if (pw.isEmpty) return 0;
  final classes = _characterClasses(pw);
  var score = 0;
  if (pw.length >= minPasswordLength) score++;
  if (pw.length >= 12) score++;
  if (classes >= 2) score++;
  if (classes >= 3) score++;
  if (_commonPasswords.contains(pw.toLowerCase())) score = score.clamp(0, 1);
  return score.clamp(0, 4);
}
