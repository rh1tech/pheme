import CoreGraphics
import CoreText
import Foundation
import UIKit

/// The drawn fallback avatar: a coloured circle with the sender's initials, for a notification
/// about someone who has no profile picture.
///
/// THIS IS A THIRD IMPLEMENTATION of one rule. The Dart one is
/// lib/src/chat/widgets/conversation_avatar.dart (the chat UI and the Android notification), the web
/// has its own, and this is iOS's — the extension is a separate process with no Dart runtime, so it
/// cannot call the original.
///
/// All three must agree, because the whole value of a hashed colour is that the same person is the
/// same colour everywhere they appear. If you change the palette or the hash, change it in all
/// three or the app starts contradicting itself between screens.
enum InitialsAvatar {
  /// The same eight colours as the Dart palette, in the same order — the index comes from the hash,
  /// so order is part of the contract, not presentation. Red and yellow are deliberately absent:
  /// they read as status, and a person is not a status.
  private static let palette: [UIColor] = [
    UIColor(red: 0x77 / 255, green: 0x40 / 255, blue: 0xEE / 255, alpha: 1),  // iris
    UIColor(red: 0xBE / 255, green: 0x4B / 255, blue: 0xDB / 255, alpha: 1),  // grape
    UIColor(red: 0x12 / 255, green: 0xB8 / 255, blue: 0x86 / 255, alpha: 1),  // teal
    UIColor(red: 0x15 / 255, green: 0xAA / 255, blue: 0xBF / 255, alpha: 1),  // cyan
    UIColor(red: 0x22 / 255, green: 0x8B / 255, blue: 0xE6 / 255, alpha: 1),  // blue
    UIColor(red: 0xFD / 255, green: 0x7E / 255, blue: 0x14 / 255, alpha: 1),  // orange
    UIColor(red: 0xE6 / 255, green: 0x49 / 255, blue: 0x80 / 255, alpha: 1),  // pink
    UIColor(red: 0x82 / 255, green: 0xC9 / 255, blue: 0x1E / 255, alpha: 1),  // lime
  ]

  /// FNV-1a over the id's UTF-16 code units.
  ///
  /// Dart's `String.codeUnits` is UTF-16, so this walks `unicodeScalars`' UTF-16 view to match it
  /// exactly — iterating bytes instead would hash the same id to a different colour for any name
  /// outside ASCII, which is the one case nobody would think to test.
  private static func color(for id: String) -> UIColor {
    var hash: UInt32 = 0x811c_9dc5
    for unit in Array(id.utf16) {
      hash ^= UInt32(unit)
      hash = hash &* 0x0100_0193
    }
    return palette[Int(hash % UInt32(palette.count))]
  }

  /// Up to two uppercase initials, or "#" when there is nothing to take them from.
  private static func initials(of label: String) -> String {
    let words = label.split(whereSeparator: { $0.isWhitespace }).prefix(2)
    let letters = words.compactMap { $0.first }.map(String.init).joined().uppercased()
    return letters.isEmpty ? "#" : letters
  }

  /// PNG bytes for the circle, or nil if it could not be drawn.
  static func png(id: String, label: String, size: CGFloat = 128) -> Data? {
    let renderer = UIGraphicsImageRenderer(size: CGSize(width: size, height: size))
    let image = renderer.image { ctx in
      color(for: id).setFill()
      ctx.cgContext.fillEllipse(in: CGRect(x: 0, y: 0, width: size, height: size))

      let text = initials(of: label)
      // 0.4 of the circle, the same proportion the Flutter widget uses, so the two look identical
      // rather than merely similar.
      let attributes: [NSAttributedString.Key: Any] = [
        .font: UIFont.systemFont(ofSize: size * 0.4, weight: .semibold),
        .foregroundColor: UIColor.white,
      ]
      let bounds = text.size(withAttributes: attributes)
      text.draw(
        at: CGPoint(x: (size - bounds.width) / 2, y: (size - bounds.height) / 2),
        withAttributes: attributes
      )
    }
    return image.pngData()
  }
}
