import Foundation
import Security

/// The one value the NotificationServiceExtension needs from the app, in a form it can actually
/// read.
///
/// The extension decrypts a message preview itself — the server ships MLS ciphertext it cannot
/// read — and that needs two things. The sealed MLS state is already shared: MlsStore writes it as
/// a FILE in the App Group container. This is the other half, the small data key that unseals it.
///
/// WHY NOT flutter_secure_storage, which already mirrors the key into the shared access group:
/// `flutter_secure_storage_darwin` does not store a value, it stores a WRAPPING of one — the
/// payload is AES-encrypted and the AES key is itself wrapped under a Secure Enclave ECIES key
/// held in a companion `fss.wrapped.<account>` item. A plain SecItemCopyMatching from the
/// extension therefore returns ciphertext belonging to a scheme it knows nothing about.
///
/// The alternative would be to reimplement that unwrap in Swift. That is the wrong answer: it
/// duplicates a private detail of a third-party plugin, and the day the plugin changes its format
/// the previews stop working with no error anywhere — the extension would decrypt garbage to a
/// nil plaintext and fall back to the generic text, which looks exactly like a message it simply
/// could not read.
///
/// So the app writes a PLAIN item, here, under its own service name, and the extension reads it
/// back the same way. Both sides are this file. Nothing infers anything about another library's
/// storage format.
///
/// On the protection level: `AfterFirstUnlock` matches what MlsStore uses for the same key, and it
/// is the weakest class that still works — a notification arrives at moments when the phone has
/// not been unlocked since it was picked up, and a key that cannot be read then is a key that
/// cannot draw a preview. The item stays inside the App Group's access group, so it is reachable
/// by this app and its extension and by nothing else.
enum SharedKeychain {
  static let accessGroup = "group.tech.rh1.pheme"
  private static let service = "tech.rh1.pheme.mls"
  private static let account = "mlsDataKey"

  private static func baseQuery() -> [String: Any] {
    [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrService as String: service,
      kSecAttrAccount as String: account,
      kSecAttrAccessGroup as String: accessGroup,
    ]
  }

  /// Stores (or replaces) the data key. Returns false rather than throwing: a build whose
  /// provisioning profile lacks the App Group fails here with errSecMissingEntitlement, and that
  /// must cost previews only — never the app's ability to hold its keys, which lives elsewhere.
  @discardableResult
  static func putDataKey(_ key: Data) -> Bool {
    // Delete-then-add rather than SecItemUpdate: an update against a missing item is an error
    // that would have to be special-cased, and this path runs rarely enough that the extra call
    // costs nothing.
    SecItemDelete(baseQuery() as CFDictionary)

    var query = baseQuery()
    query[kSecValueData as String] = key
    query[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
    return SecItemAdd(query as CFDictionary, nil) == errSecSuccess
  }

  /// Reads the data key back, or nil when it is not there or cannot be read yet.
  static func dataKey() -> Data? {
    var query = baseQuery()
    query[kSecReturnData as String] = true
    query[kSecMatchLimit as String] = kSecMatchLimitOne

    var out: CFTypeRef?
    guard SecItemCopyMatching(query as CFDictionary, &out) == errSecSuccess else { return nil }
    return out as? Data
  }

  /// Removes it. Called when the app wipes its key material, so a stale key cannot outlive the
  /// state it belonged to.
  static func clear() {
    SecItemDelete(baseQuery() as CFDictionary)
  }
}
