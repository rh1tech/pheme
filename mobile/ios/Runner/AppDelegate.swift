import Flutter
import PushKit
import UIKit
import flutter_callkit_incoming

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate, PKPushRegistryDelegate {
  private var voipRegistry: PKPushRegistry?

  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    registerForVoIPPushes()
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)
    // The messenger comes from a registrar rather than the bridge: FlutterImplicitEngineBridge
    // exposes the plugin registry, not the messenger itself.
    if let registrar = engineBridge.pluginRegistry.registrar(forPlugin: "PhemeSharedKeychain") {
      registerSharedKeychainChannel(registrar.messenger())
    }
  }

  /// Lets Dart hand the MLS data key to the NotificationServiceExtension.
  ///
  /// MlsStore owns the key and keeps it in the app's own keychain group as it always has; this only
  /// mirrors it, as a plain item, into the shared App Group where the extension can read it. See
  /// SharedKeychain for why flutter_secure_storage cannot serve that purpose.
  ///
  /// Every result is a plain bool. A failure here means no previews on this device — never an
  /// error the app has to handle — so nothing on the Dart side is asked to catch anything.
  private func registerSharedKeychainChannel(_ messenger: FlutterBinaryMessenger) {
    let channel = FlutterMethodChannel(
      name: "tech.rh1.pheme/shared_keychain",
      binaryMessenger: messenger
    )
    channel.setMethodCallHandler { call, result in
      switch call.method {
      case "putDataKey":
        guard let data = (call.arguments as? FlutterStandardTypedData)?.data else {
          result(false)
          return
        }
        result(SharedKeychain.putDataKey(data))
      case "clear":
        SharedKeychain.clear()
        result(true)
      default:
        result(FlutterMethodNotImplemented)
      }
    }
  }

  /// PushKit is the only way to ring a sleeping iPhone.
  ///
  /// It is a separate registry from ordinary remote notifications and issues a SEPARATE TOKEN, which
  /// is why the server stores a VoIP token alongside the FCM one and sends calls straight to APNs.
  /// Firebase cannot reach this: it has no way to be handed a PushKit token, it cannot set the
  /// `<bundle>.voip` topic, and `apns-push-type: voip` is not part of its contract.
  private func registerForVoIPPushes() {
    let registry = PKPushRegistry(queue: .main)
    registry.delegate = self
    registry.desiredPushTypes = [.voIP]
    voipRegistry = registry
  }

  func pushRegistry(
    _ registry: PKPushRegistry,
    didUpdate credentials: PKPushCredentials,
    for type: PKPushType
  ) {
    guard type == .voIP else { return }
    let token = credentials.token.map { String(format: "%02x", $0) }.joined()
    // Handed to Dart, which registers it with the server the next time the device is registered.
    SwiftFlutterCallkitIncomingPlugin.sharedInstance?.setDevicePushTokenVoIP(token)
  }

  func pushRegistry(
    _ registry: PKPushRegistry,
    didInvalidatePushTokenFor type: PKPushType
  ) {
    guard type == .voIP else { return }
    SwiftFlutterCallkitIncomingPlugin.sharedInstance?.setDevicePushTokenVoIP("")
  }

  /// A VoIP push arrived.
  ///
  /// ---------------------------------------------------------------------------------------------
  /// THE ONE RULE: this MUST report an incoming call to CallKit, synchronously, before it does
  /// anything else and before it calls the completion handler. Not most of the time — every time,
  /// including for a call that has already been cancelled. iOS kills an app that takes a VoIP push
  /// without reporting a call, and after a few offences it stops delivering VoIP pushes to that
  /// install altogether. There is no recovering from that except a reinstall.
  ///
  /// So there is no `guard` here that returns early without reporting, and there must never be one.
  /// A payload we cannot understand still gets reported, and then immediately ended.
  /// ---------------------------------------------------------------------------------------------
  ///
  /// Note what is NOT here: the invite. The push carries who is calling and which call it is, and
  /// nothing else — the SDP is in the server's mailbox, sealed under a key derived from the
  /// conversation's MLS group. Fetching it needs the network, the keychain and an MLS catch-up, none
  /// of which can happen before the call is reported. Dart does that work while the phone is already
  /// ringing, and CallService's watchdog ends the call if it does not finish in time.
  func pushRegistry(
    _ registry: PKPushRegistry,
    didReceiveIncomingPushWith payload: PKPushPayload,
    for type: PKPushType,
    completion: @escaping () -> Void
  ) {
    guard type == .voIP else {
      completion()
      return
    }

    let data = payload.dictionaryPayload
    let callId = data["callId"] as? String ?? UUID().uuidString
    let callerName = data["callerName"] as? String ?? "Pheme"
    let conversationId = data["conversationId"] as? String ?? ""
    let kind = data["kind"] as? String ?? "call"

    let call = flutter_callkit_incoming.Data(
      id: callId,
      nameCaller: callerName,
      handle: conversationId,
      type: 0 // audio
    )
    call.appName = "Pheme"
    call.supportsVideo = false
    call.duration = 35_000 // the caller gives up at 35s; both ends should agree
    call.extra = ["conversationId": conversationId]

    // Report FIRST. Always.
    SwiftFlutterCallkitIncomingPlugin.sharedInstance?.showCallkitIncoming(call, fromPushKit: true)

    // A cancellation is still a VoIP push, and Apple gives it no exemption from the rule above. The
    // sanctioned way to take a ring back is to report the call and then end it at once — CallKit is
    // built for exactly this, and nothing user-visible appears if the end lands promptly.
    if kind == "call-cancel" {
      SwiftFlutterCallkitIncomingPlugin.sharedInstance?.endCall(call)
    }

    completion()
  }
}
