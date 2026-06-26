import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';

import '../theme.dart';

/// The Pheme wordmark: the broadcast glyph on the violet→grape gradient tile,
/// next to the name. The glyph is the shared brand SVG (assets/brand/glyph.svg)
/// — the exact same mark used by the launcher icon and the web favicon, so all
/// surfaces are pixel-identical.
class BrandLogo extends StatelessWidget {
  const BrandLogo({super.key, this.size = 28, this.showWordmark = true});

  final double size;
  final bool showWordmark;

  @override
  Widget build(BuildContext context) {
    final tile = Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        gradient: kBrandGradient,
        borderRadius: BorderRadius.circular(size * 0.28),
      ),
      alignment: Alignment.center,
      child: SvgPicture.asset(
        'assets/brand/glyph.svg',
        width: size * 0.66,
        height: size * 0.66,
      ),
    );
    if (!showWordmark) return tile;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        tile,
        SizedBox(width: size * 0.4),
        Text(
          'Pheme',
          style: TextStyle(
            fontSize: size * 0.7,
            fontWeight: FontWeight.w700,
            letterSpacing: -0.5,
          ),
        ),
      ],
    );
  }
}
