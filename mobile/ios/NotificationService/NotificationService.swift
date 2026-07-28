import Intents
import UserNotifications
import os

/// Turns "New message" into the actual message, on the device, without the server ever being able
/// to read it.
///
/// The server sends MLS ciphertext it cannot decrypt itself, along with generic text to fall back
/// on. iOS hands that push to this extension first — because the payload sets `mutable-content` —
/// and whatever it puts in `bestAttemptContent` is what the user sees.
///
/// THE RULE THIS FILE EXISTS UNDER: every path ends in a notification. A failure here must show the
/// server's generic text, never nothing and never an error. iOS gives the extension roughly 30
/// seconds and then calls `serviceExtensionTimeWillExpire`, after which it displays whatever the
/// content is at that moment — so the fallback is prepared FIRST and only overwritten on success.
///
/// What it deliberately does NOT do: advance any MLS state. `pheme_preview_decrypt` opens a COPY
/// via PreviewClient, which has no `export_state` and refuses Commits, so it cannot move the epoch.
/// The app keeps its own unconsumed key and reads the message again, for real, later. "A message
/// decrypts exactly once" is a property of a copy — see crates/pheme-mls and mobile/rust/src/cabi.rs,
/// where tests pin exactly this.
/// Where this extension's diagnostics go.
///
/// An extension has no console of its own and no debugger attached in normal use, so a failure here
/// is invisible unless it says so: the notification simply arrives looking ordinary. Read with
///   log stream --device --predicate 'subsystem == "tech.rh1.pheme.notificationservice"'
private let log = Logger(subsystem: "tech.rh1.pheme.notificationservice", category: "preview")

final class NotificationService: UNNotificationServiceExtension {
  private var contentHandler: ((UNNotificationContent) -> Void)?
  private var bestAttemptContent: UNMutableNotificationContent?

  /// Guards the one thing that must happen exactly once.
  ///
  /// Two paths can finish this extension — the avatar fetch completing, and iOS calling
  /// serviceExtensionTimeWillExpire when the budget runs out — and they RACE. Calling the content
  /// handler twice is undefined; the lock makes whichever arrives first the only one that counts.
  private let lock = NSLock()

  private func deliver(_ content: UNNotificationContent) {
    lock.lock()
    let handler = contentHandler
    contentHandler = nil
    lock.unlock()
    handler?(content)
  }

  override func didReceive(
    _ request: UNNotificationRequest,
    withContentHandler contentHandler: @escaping (UNNotificationContent) -> Void
  ) {
    self.contentHandler = contentHandler
    let content = request.content.mutableCopy() as? UNMutableNotificationContent
    bestAttemptContent = content

    // No content to mutate: hand back exactly what arrived rather than dropping it.
    guard let content else {
      deliver(request.content)
      return
    }

    if let body = Self.preview(from: content.userInfo) {
      content.body = body
    }

    // The avatar needs the network, so it is the only part of this that waits. The decrypt above
    // has already happened either way — a slow or missing picture must never cost the message text.
    Self.withSenderAvatar(content) { [weak self] finished in
      self?.deliver(finished)
    }
  }

  /// Time is up. Deliver the generic text rather than losing the notification.
  override func serviceExtensionTimeWillExpire() {
    if let bestAttemptContent {
      deliver(bestAttemptContent)
    }
  }

  /// Turns the notification into a COMMUNICATION notification — the sender's name and their avatar
  /// as a circular portrait, the way Messages renders a chat — and hands the result back.
  ///
  /// iOS has no field for a notification avatar. The only route is to attach an INSendMessageIntent
  /// to the content, which is what `updating(from:)` does; Android reaches the same place with
  /// Person/MessagingStyle (see _showChatNotification in push_service.dart), and the two are
  /// deliberately equivalent in what the user ends up seeing.
  ///
  /// Every failure calls back with the content unchanged. A notification with no picture is a
  /// working notification; one that never arrives because a CDN was slow is not — so the fetch is
  /// capped hard, and the completion runs exactly once on every path.
  private static func withSenderAvatar(
    _ content: UNMutableNotificationContent,
    completion: @escaping (UNNotificationContent) -> Void
  ) {
    let info = content.userInfo
    // A title is the one thing this cannot do without: it is both the name shown and where the
    // initials come from. No name means the recipient asked not to be told who is messaging them,
    // and drawing a portrait for an anonymous sender would leak exactly what that setting withholds.
    guard !content.title.isEmpty else {
      completion(content)
      return
    }

    // What the fallback circle's colour is hashed from — the sender where the server names one, so
    // the circle matches the colour that person already has in the chat list, and the conversation
    // otherwise (right for a group, and the best a server predating senderId can offer).
    let colorId =
      (info["senderId"] as? String)
      ?? (info["conversationId"] as? String)
      ?? ""

    // No picture: draw the initials instead of falling back to the app icon, which is what an
    // avatar-less sender used to get here and empty space on Android.
    guard
      let urlString = info["senderAvatar"] as? String,
      let url = URL(string: urlString)
    else {
      log.info("no senderAvatar in payload — drawing initials")
      let drawn = InitialsAvatar.png(id: colorId, label: content.title)
      completion(drawn.flatMap { try? applyIntent(to: content, avatar: $0) } ?? content)
      return
    }

    var request = URLRequest(url: url)
    // The extension's whole budget is about 30 seconds, and the text is already in hand. Five is
    // generous for an avatar and leaves the rest for the system.
    request.timeoutInterval = 5

    URLSession.shared.dataTask(with: request) { data, _, _ in
      // A picture that exists but did not arrive — slow CDN, no network on the push path — is no
      // reason to show the app icon when a circle can be drawn from what is already in hand.
      if data?.isEmpty != false {
        log.error("avatar fetch returned nothing — drawing initials instead")
      }
      let bytes = (data?.isEmpty == false) ? data! : InitialsAvatar.png(id: colorId, label: content.title)
      guard
        let bytes,
        let updated = try? applyIntent(to: content, avatar: bytes)
      else {
        completion(content)
        return
      }
      completion(updated)
    }.resume()
  }

  /// Builds the intent and folds it into the content. Throws if the entitlement is missing, which
  /// is why the caller treats any error as "no picture" rather than as a failure.
  private static func applyIntent(
    to content: UNMutableNotificationContent, avatar: Data
  ) throws -> UNNotificationContent {
    let conversationId = (content.userInfo["conversationId"] as? String) ?? ""

    // The sender is identified by the conversation rather than by a user id, which the push does
    // not carry. That is enough for what this does: it groups a chat's notifications together and
    // gives iOS a stable identity to hang the avatar on.
    let handle = INPersonHandle(value: conversationId, type: .unknown)
    let sender = INPerson(
      personHandle: handle,
      nameComponents: nil,
      displayName: content.title,
      image: INImage(imageData: avatar),
      contactIdentifier: nil,
      customIdentifier: conversationId
    )

    let intent = INSendMessageIntent(
      recipients: nil,
      outgoingMessageType: .outgoingMessageText,
      content: nil,
      speakableGroupName: nil,
      conversationIdentifier: conversationId,
      serviceName: nil,
      sender: sender,
      attachments: nil
    )

    // The picture has to be set on the INTENT as well as on the INPerson.
    //
    // Setting it only on the sender is the documented-looking thing to do and it silently does not
    // work: the notification renders as a communication notification, with the name, and wearing
    // the app icon where the portrait should be. No error is thrown and nothing is logged — the
    // avatar just is not there. setImage(_:forParameterNamed:) is what actually attaches it.
    intent.setImage(INImage(imageData: avatar), forParameterNamed: \.sender)

    // Donating is what lets the conversation appear in Focus settings and the share sheet, so the
    // user can allow this chat through a Focus mode. Incoming, because we are receiving.
    let interaction = INInteraction(intent: intent, response: nil)
    interaction.direction = .incoming
    interaction.donate(completion: nil)

    do {
      let updated = try content.updating(from: intent)
      log.info("avatar applied")
      return updated
    } catch {
      // The one failure worth naming. It means the communication-notifications entitlement is not
      // in force for the containing app, and the symptom is a notification that looks completely
      // normal apart from wearing the app icon.
      log.error("updating(from:) failed — no avatar: \(error.localizedDescription, privacy: .public)")
      throw error
    }
  }

  /// The decrypted body, or nil to keep whatever the server sent.
  ///
  /// Every failure is nil and none of them is exceptional: a push with no ciphertext (the ordinary
  /// case for anyone who has not asked for previews), a device with no key material yet, a message
  /// for a group this device never joined, control traffic, or a state blob written by a newer
  /// build. The C ABI returns null for all of them and so does this.
  private static func preview(from userInfo: [AnyHashable: Any]) -> String? {
    guard
      let ciphertextB64 = userInfo["ciphertext"] as? String,
      let ciphertext = Data(base64Encoded: ciphertextB64),
      !ciphertext.isEmpty
    else { return nil }

    guard let dataKey = SharedKeychain.dataKey(), let state = sealedState() else { return nil }

    // The payload lists groups comma-separated; the C ABI wants them newline-separated. A
    // conversation can have more than one, because a retired group's messages still decrypt under
    // its old id.
    let groupIds = ((userInfo["groupIds"] as? String) ?? "")
      .split(separator: ",")
      .joined(separator: "\n")
    guard !groupIds.isEmpty else { return nil }

    guard let plaintext = decrypt(state: state, key: dataKey, groups: groupIds, ciphertext: ciphertext)
    else { return nil }

    // Matches _bodyOf in notification_preview.dart: the plaintext is a JSON object and the preview
    // is its `body`. An empty body is nil on purpose — a photo with no caption has nothing to show,
    // and inventing the word "Photo" would be a claim about content this path should not make.
    guard
      let object = try? JSONSerialization.jsonObject(with: plaintext) as? [String: Any],
      let body = object["body"] as? String,
      !body.isEmpty
    else { return nil }

    return body
  }

  /// The sealed MLS state, read from the App Group container the app writes it to.
  ///
  /// A plain file read. The extension has its own container and cannot see the app's private
  /// directory, which is precisely why MlsStore puts the state here.
  private static func sealedState() -> Data? {
    guard
      let container = FileManager.default.containerURL(
        forSecurityApplicationGroupIdentifier: SharedKeychain.accessGroup
      )
    else { return nil }
    return try? Data(contentsOf: container.appendingPathComponent("mls/mls.state"))
  }

  /// The FFI call, with every pointer confined to this function.
  private static func decrypt(
    state: Data, key: Data, groups: String, ciphertext: Data
  ) -> Data? {
    var outLen = 0
    let raw: UnsafeMutablePointer<UInt8>? = state.withUnsafeBytes { statePtr in
      key.withUnsafeBytes { keyPtr in
        ciphertext.withUnsafeBytes { ctPtr in
          groups.withCString { groupsPtr in
            pheme_preview_decrypt(
              statePtr.bindMemory(to: UInt8.self).baseAddress, state.count,
              keyPtr.bindMemory(to: UInt8.self).baseAddress, key.count,
              groupsPtr,
              ctPtr.bindMemory(to: UInt8.self).baseAddress, ciphertext.count,
              &outLen
            )
          }
        }
      }
    }

    guard let raw, outLen > 0 else { return nil }
    // Copy before freeing: the buffer belongs to Rust and must go back with the same length.
    defer { pheme_preview_free(raw, outLen) }
    return Data(bytes: raw, count: outLen)
  }
}
