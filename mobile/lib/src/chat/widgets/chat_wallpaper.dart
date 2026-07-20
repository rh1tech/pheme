import 'package:flutter/material.dart';

/// The background a feed sits on — the same one the web draws.
///
/// Three layers, in the order the CSS stacks them: a flat colour, the app's own motifs tiled over
/// it, and two soft radial washes, violet from the top left and magenta from the bottom right.
/// The pattern is Pheme's own artwork (a bubble, a bell, a paper plane, a star, an @, a bolt, an
/// envelope, the double tick), scattered at varied rotation and scale so it reads as a wallpaper
/// rather than a spreadsheet — and it tiles seamlessly at any offset.
///
/// Rasterised from the same SVG the web serves, rather than redrawn: two versions of a pattern
/// drift apart the moment either is touched, and a wallpaper that is ALMOST the same on two
/// clients looks like a mistake rather than a family resemblance.
///
/// Used by chats, channels and the comments under a post, so all three read as one app.
class ChatWallpaper extends StatelessWidget {
  const ChatWallpaper({super.key, required this.child});

  final Widget child;

  /// The values are the web's, taken from chat.css rather than sampled by eye:
  /// --pheme-chat-wallpaper-bg, and the two radial-gradient stops beside it.
  static const _lightBg = Color(0xFFF5F2FC);
  static const _darkBg = Color(0xFF15161A);

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;

    return Container(
      color: dark ? _darkBg : _lightBg,
      child: Stack(
        fit: StackFit.expand,
        children: [
          // The motifs. Low opacity in both themes: it is a texture, and anything you can read
          // individually competes with the messages on top of it.
          Opacity(
            opacity: dark ? 0.35 : 0.5,
            child: Image.asset(
              dark
                  ? 'assets/chat/chat-pattern-dark.png'
                  : 'assets/chat/chat-pattern-light.png',
              repeat: ImageRepeat.repeat,
              // Nearest-neighbour would shimmer as the list scrolls under it.
              filterQuality: FilterQuality.medium,
            ),
          ),
          // Violet from the top left.
          DecoratedBox(
            decoration: BoxDecoration(
              gradient: RadialGradient(
                center: const Alignment(-0.75, -1),
                radius: 1.1,
                colors: [
                  const Color(0xFF7740EE).withValues(alpha: dark ? 0.10 : 0.11),
                  Colors.transparent,
                ],
                stops: const [0, 0.6],
              ),
            ),
          ),
          // Magenta from the bottom right.
          DecoratedBox(
            decoration: BoxDecoration(
              gradient: RadialGradient(
                center: const Alignment(1, 1),
                radius: 1.0,
                colors: [
                  const Color(0xFFBE4BDB).withValues(alpha: dark ? 0.06 : 0.10),
                  Colors.transparent,
                ],
                stops: const [0, 0.55],
              ),
            ),
          ),
          child,
        ],
      ),
    );
  }
}

/// The bubble colours and shadow the web uses, so a bubble looks the same wherever it is drawn.
///
/// The shadow is what was missing most: a flat rectangle on a patterned background reads as a hole
/// in the wallpaper, where a shadowed one sits on top of it.
class BubbleStyle {
  const BubbleStyle._();

  static Color background(BuildContext context) =>
      Theme.of(context).brightness == Brightness.dark
      ? const Color(0xFF343841)
      : Colors.white;

  /// --pheme-bubble-shadow. Two stops in light, one in dark — a dark theme needs far less to lift
  /// a surface off its background, and the light one's second stop would read as smudge.
  static List<BoxShadow> shadow(BuildContext context) =>
      Theme.of(context).brightness == Brightness.dark
      ? const [
          BoxShadow(
            color: Color(0x66000000),
            blurRadius: 2,
            offset: Offset(0, 1),
          ),
        ]
      : const [
          BoxShadow(
            color: Color(0x213A1E78),
            blurRadius: 2,
            offset: Offset(0, 1),
          ),
          BoxShadow(
            color: Color(0x123A1E78),
            blurRadius: 6,
            offset: Offset(0, 2),
          ),
        ];
}
