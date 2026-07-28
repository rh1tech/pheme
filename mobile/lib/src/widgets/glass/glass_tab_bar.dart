// The tab bar, on both platforms.

import 'package:flutter/material.dart';

import 'glass_surface.dart';
import 'glass_tokens.dart';

/// One destination in a [GlassTabBar].
@immutable
class GlassTab {
  const GlassTab({
    required this.icon,
    required this.selectedIcon,
    required this.label,
  });

  final IconData icon;
  final IconData selectedIcon;
  final String label;
}

/// A floating glass tab bar: a pill above the home indicator, with the list scrolling underneath it.
///
/// It replaces a [CupertinoTabBar] on iOS and a Material 3 [NavigationBar] on Android — two bars
/// that were pinned to the bottom edge, sized differently, and drew selection differently (a tint
/// on one, a filled capsule on the other). Floating it off the edge is what makes the blur worth
/// having: there is content behind it, moving, and the bar reads as sitting above the screen rather
/// than as a strip cut out of it.
class GlassTabBar extends StatelessWidget {
  const GlassTabBar({
    super.key,
    required this.tabs,
    required this.currentIndex,
    required this.onSelected,
  });

  final List<GlassTab> tabs;
  final int currentIndex;
  final ValueChanged<int> onSelected;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.fromLTRB(
        GlassMetrics.gutter,
        0,
        GlassMetrics.gutter,
        MediaQuery.paddingOf(context).bottom + GlassMetrics.gutter,
      ),
      child: GlassSurface(
        floating: true,
        borderRadius: BorderRadius.circular(GlassMetrics.tabBarHeight / 2),
        child: SizedBox(
          height: GlassMetrics.tabBarHeight,
          child: Row(
            // STRETCH, not the default centre. A centred Row hands its children a loose height
            // constraint, so the selected capsule sized itself to the icon inside it — a small
            // lozenge adrift in a tall bar — rather than filling its half of it. Nothing about the
            // capsule's own padding could fix that; it was never being given the height.
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (var i = 0; i < tabs.length; i++)
                Expanded(
                  child: _TabButton(
                    tab: tabs[i],
                    selected: i == currentIndex,
                    onPressed: () => onSelected(i),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class _TabButton extends StatelessWidget {
  const _TabButton({
    required this.tab,
    required this.selected,
    required this.onPressed,
  });

  final GlassTab tab;
  final bool selected;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    final palette = GlassPalette.of(context);
    final color = selected ? palette.accent : palette.mutedForeground;

    return Semantics(
      button: true,
      selected: selected,
      label: tab.label,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onPressed,
        child: ExcludeSemantics(
          child: Padding(
            // Just enough to keep a rim of the bar showing round the selected capsule. At 6 the
            // capsule read as a small button sitting inside a large bar rather than as one half of
            // it being lit.
            padding: const EdgeInsets.all(4),
            child: AnimatedContainer(
              duration: const Duration(milliseconds: 180),
              curve: Curves.easeOut,
              decoration: BoxDecoration(
                color: selected
                    ? palette.accent.withValues(alpha: 0.14)
                    : Colors.transparent,
                borderRadius: BorderRadius.circular(
                  GlassMetrics.tabBarHeight / 2,
                ),
              ),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Icon(
                    selected ? tab.selectedIcon : tab.icon,
                    size: 21,
                    color: color,
                  ),
                  const SizedBox(width: 7),
                  Flexible(
                    child: Text(
                      tab.label,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        fontSize: 13.5,
                        fontWeight: FontWeight.w600,
                        color: color,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
