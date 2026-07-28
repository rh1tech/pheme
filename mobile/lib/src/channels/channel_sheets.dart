// The one sheet every channel action is shown in.
//
// These were tabs — Send, Subscribers, Keys, Settings — sitting alongside the messages, so a
// channel opened onto a tab bar where a chat opens onto its messages. Four of the five were things
// you do to a channel occasionally; one was the channel itself. Giving them equal billing put the
// reason you opened the screen behind a row of things you mostly did not want.
//
// They are sheets now, reached from the ⋮ menu, which is where a chat keeps the same kind of
// action. And they are ONE sheet: the tall ones used to carry a title in a different weight, a
// close button, and a rule underneath, while the short ones carried none of those — six entries in
// one menu opening onto two visibly different surfaces. A sheet is already dismissed by dragging it
// or by tapping outside, so the ✕ was a third way of doing what the sheet does for itself, and the
// rule was drawing a boundary the padding already made plain.

import 'package:flutter/material.dart';

import '../widgets/adaptive/adaptive.dart';

/// Hosts one of the channel's surfaces in a sheet.
///
/// [fill] gives it most of the screen, for the surfaces that hold a list of unknown length. Without
/// it the sheet is only as tall as what it contains, which is what a single decision wants.
///
/// [footer] is a pinned full-width action at the foot — the primary thing this sheet exists to do,
/// kept where the thumb is and out of the scrolling content, so it stays reachable however long the
/// list above it grows.
Future<T?> showChannelSheet<T>(
  BuildContext context, {
  required String title,
  required Widget child,
  Widget? footer,
  bool fill = false,
}) {
  return showModalBottomSheet<T>(
    context: context,
    isScrollControlled: true,
    // Without this the sheet runs under the status bar and the notch on a tall screen — the same
    // reason the new-chat sheet asks for it.
    useSafeArea: true,
    showDragHandle: true,
    builder: (context) {
      final content = Column(
        mainAxisSize: fill ? MainAxisSize.max : MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(20, 4, 20, 16),
            child: Text(
              title,
              style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w600),
            ),
          ),
          // A tall sheet's content brings its own padding — it is a list, and a list that stops
          // short of the edge looks inset rather than full. A compact one is a paragraph and a
          // button, and gets the same gutter as the title above it.
          if (fill)
            Expanded(child: child)
          else
            Flexible(
              child: Padding(
                padding: const EdgeInsets.fromLTRB(20, 0, 20, 8),
                child: child,
              ),
            ),
          if (footer != null)
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 12, 20, 4),
              child: footer,
            ),
        ],
      );

      return SafeArea(
        top: false,
        child: Padding(
          // Most of these never raise a keyboard, but a sheet that ignores the inset is a trap
          // waiting for the first one that does.
          padding: EdgeInsets.only(
            bottom: MediaQuery.viewInsetsOf(context).bottom,
          ),
          child: fill
              ? FractionallySizedBox(heightFactor: 0.92, child: content)
              : content,
        ),
      );
    },
  );
}

/// The full-width accent button a [showChannelSheet] footer is made of.
///
/// Named rather than left to each caller so the two sheets that have one cannot drift apart: it is
/// the same button doing the same job in the same place.
class ChannelSheetAction extends StatelessWidget {
  const ChannelSheetAction({
    super.key,
    required this.label,
    required this.onPressed,
    this.busy = false,
  });

  final String label;
  final VoidCallback? onPressed;
  final bool busy;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: double.infinity,
      child: AdaptiveButton.filled(
        onPressed: busy ? null : onPressed,
        child: busy ? const AdaptiveProgress(size: 18) : Text(label),
      ),
    );
  }
}
