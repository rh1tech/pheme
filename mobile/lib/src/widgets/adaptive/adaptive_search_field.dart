import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../l10n/app_localizations.dart';
import '../glass/glass_tokens.dart';
import 'platform.dart';

/// The app's search field: a grey capsule with a magnifier at the head, a clear button once there is
/// something to clear, and a Cancel beside it while it has focus.
///
/// It used to be two fields — a [CupertinoSearchTextField] on one platform and a filled Material
/// [TextField] on the other — on the theory that each platform has its own instantly recognisable
/// search box. They are both a grey capsule with a magnifier in it; what they are not is the same
/// grey, the same capsule or the same height, so the two builds disagreed about the size of a row
/// that sits at the top of the app's two main screens. One field, and only the glyphs follow the
/// platform.
class AdaptiveSearchField extends StatefulWidget {
  const AdaptiveSearchField({
    super.key,
    required this.controller,
    required this.placeholder,
    this.onChanged,
  });

  final TextEditingController controller;
  final String placeholder;
  final ValueChanged<String>? onChanged;

  @override
  State<AdaptiveSearchField> createState() => _AdaptiveSearchFieldState();

  /// The field's height. Public because a page that PINS this one has to reserve exactly this much
  /// room for it above its list, and a number guessed at the call site drifts from this one.
  static const double height = 40;
}

class _AdaptiveSearchFieldState extends State<AdaptiveSearchField> {
  final _focus = FocusNode();

  @override
  void initState() {
    super.initState();
    _focus.addListener(_onFocusChanged);
  }

  void _onFocusChanged() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _focus.removeListener(_onFocusChanged);
    _focus.dispose();
    super.dispose();
  }

  /// Clears the query AND gives up focus — the two halves of "I am done searching".
  void _cancel() {
    widget.controller.clear();
    widget.onChanged?.call('');
    _focus.unfocus();
  }

  @override
  Widget build(BuildContext context) {
    final ios = isCupertino(context);
    final l10n = AppLocalizations.of(context);
    final scheme = Theme.of(context).colorScheme;
    final muted = scheme.onSurfaceVariant;

    final field = Container(
      height: AdaptiveSearchField.height,
      padding: const EdgeInsets.symmetric(horizontal: 10),
      decoration: BoxDecoration(
        color: scheme.onSurface.withValues(alpha: 0.06),
        borderRadius: BorderRadius.circular(AdaptiveSearchField.height / 2),
      ),
      child: Row(
        children: [
          Icon(
            ios ? CupertinoIcons.search : Icons.search,
            size: 18,
            color: muted,
          ),
          const SizedBox(width: 6),
          Expanded(
            child: TextField(
              controller: widget.controller,
              focusNode: _focus,
              onChanged: widget.onChanged,
              textInputAction: TextInputAction.search,
              onSubmitted: (_) => _focus.unfocus(),
              style: const TextStyle(fontSize: 15),
              decoration: InputDecoration(
                isDense: true,
                hintText: widget.placeholder,
                hintStyle: TextStyle(fontSize: 15, color: muted),
                border: InputBorder.none,
                enabledBorder: InputBorder.none,
                focusedBorder: InputBorder.none,
                contentPadding: EdgeInsets.zero,
              ),
            ),
          ),
          // Only while there is a query. A permanent clear button is a control that does nothing
          // most of the time, and it is the widest thing in a field this short.
          ValueListenableBuilder<TextEditingValue>(
            valueListenable: widget.controller,
            builder: (context, value, _) => value.text.isEmpty
                ? const SizedBox.shrink()
                : GestureDetector(
                    behavior: HitTestBehavior.opaque,
                    onTap: () {
                      widget.controller.clear();
                      widget.onChanged?.call('');
                    },
                    child: Padding(
                      padding: const EdgeInsets.only(left: 6),
                      child: Icon(
                        ios
                            ? CupertinoIcons.clear_circled_solid
                            : Icons.cancel_rounded,
                        size: GlassMetrics.icon,
                        color: muted,
                      ),
                    ),
                  ),
          ),
        ],
      ),
    );

    // Cancel, beside the field, only while it has focus.
    //
    // Without it there was NO WAY OUT of a search on a phone: the field takes focus on a tap, the
    // keyboard covers the bottom of the screen, and nothing on the remaining screen gives the focus
    // back — not the clear button, which empties the query but keeps the caret, and not a tap on the
    // list, which is a scrollable rather than a dismiss target. The escape has to be visible, and it
    // has to be next to the thing it escapes from.
    return Row(
      children: [
        Expanded(child: field),
        AnimatedSize(
          duration: const Duration(milliseconds: 160),
          curve: Curves.easeOut,
          child: _focus.hasFocus
              ? GestureDetector(
                  behavior: HitTestBehavior.opaque,
                  onTap: _cancel,
                  child: Padding(
                    padding: const EdgeInsets.only(left: 4),
                    child: SizedBox(
                      // The FIELD's height, not minTapTarget. At 44 against a 40pt field the row
                      // grew by 4pt the moment the field took focus, and everything below it —
                      // the whole chat list — stepped down by that much. The generous horizontal
                      // padding keeps this comfortable to hit; a tap target that shoves the page
                      // is not worth the extra 4pt.
                      height: AdaptiveSearchField.height,
                      child: Center(
                        child: Padding(
                          padding: const EdgeInsets.symmetric(horizontal: 8),
                          child: Text(
                            l10n.t('common.cancel'),
                            style: TextStyle(
                              fontSize: 15,
                              fontWeight: FontWeight.w500,
                              color: scheme.primary,
                            ),
                          ),
                        ),
                      ),
                    ),
                  ),
                )
              : const SizedBox.shrink(),
        ),
      ],
    );
  }
}
