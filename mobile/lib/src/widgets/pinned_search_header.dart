// A search field that stays put while the list moves under it.
//
// It used to ride inside the list as an ordinary row, on the reasoning that a field which hides on
// one scroll gesture and returns on another is one more thing moving on screen. That is true of a
// field that hides ITSELF; it is not what carrying it in the list produced. In the list it simply
// scrolled away with the rows, so searching a long list of chats meant scrolling back to the top
// first to reach the box that would have saved the scrolling.
//
// Pinned below the bar instead, on its own glass, so the rows pass beneath it the way they pass
// beneath the bar above it — which is the same argument the chrome already makes everywhere else.

import 'package:flutter/material.dart';

import 'adaptive/adaptive_search_field.dart';
import 'glass/glass.dart';

class PinnedSearchHeader extends StatelessWidget {
  const PinnedSearchHeader({
    super.key,
    required this.controller,
    required this.placeholder,
    this.onChanged,
  });

  final TextEditingController controller;
  final String placeholder;
  final ValueChanged<String>? onChanged;

  /// The room a list has to leave for this above its first row.
  ///
  /// Derived from the field's own height rather than guessed, because the two numbers have to agree
  /// exactly: too little and the first chat hides behind the field, too much and the list opens on a
  /// band of nothing.
  static const double extent =
      GlassMetrics.gap + AdaptiveSearchField.height + GlassMetrics.gap;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(
        GlassMetrics.gutter,
        GlassMetrics.gap,
        GlassMetrics.gutter,
        GlassMetrics.gap,
      ),
      child: GlassSurface(
        floating: true,
        borderRadius: BorderRadius.circular(AdaptiveSearchField.height / 2),
        child: AdaptiveSearchField(
          controller: controller,
          placeholder: placeholder,
          onChanged: onChanged,
        ),
      ),
    );
  }
}
