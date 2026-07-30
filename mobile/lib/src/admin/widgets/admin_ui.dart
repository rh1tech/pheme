// The pieces every admin list is built from.
//
// Six screens share one shape — search at the top, a page of rows, a pager at the foot — and the
// web panel proves what happens when each is written separately: six slightly different paddings,
// three ideas of what "no results" looks like, and paging arithmetic repeated per screen. So the
// shape lives here once and the screens supply only the rows.

import 'dart:async';

import 'package:flutter/material.dart';

import '../../l10n/app_localizations.dart';
import '../../widgets/adaptive/adaptive.dart';
import '../../widgets/error_view.dart';
import '../../widgets/glass/glass.dart';
import '../../widgets/pinned_search_header.dart';
import '../admin_models.dart';

/// How many rows one page of an admin listing holds. Matches the web panel's ADMIN_PAGE_LIMIT, so
/// "page 3" means the same rows in both.
const adminPageLimit = 20;

/// A labelled figure from the overview.
class AdminStatTile extends StatelessWidget {
  const AdminStatTile({super.key, required this.label, required this.value});

  final String label;
  final int value;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return AdaptiveCard(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            _grouped(value),
            style: theme.textTheme.headlineSmall?.copyWith(
              fontWeight: FontWeight.w600,
              // Tabular figures, so a column of these does not jitter as the numbers change under
              // a refresh — the one place in the app where digits are the content.
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
          const SizedBox(height: 2),
          Text(
            label,
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      ),
    );
  }

  /// Thin-space thousands separators. Not intl's number format: that is locale-aware and would put
  /// a comma in one language and a dot in another, and these are counts, not money.
  static String _grouped(int n) {
    final digits = n.abs().toString();
    final buf = StringBuffer(n < 0 ? '-' : '');
    for (var i = 0; i < digits.length; i++) {
      if (i > 0 && (digits.length - i) % 3 == 0) buf.write(' ');
      buf.write(digits[i]);
    }
    return buf.toString();
  }
}

/// A small coloured word — a role, a status, an invitation's fate.
class AdminBadge extends StatelessWidget {
  const AdminBadge({super.key, required this.label, this.color});

  final String label;

  /// Null means neutral, which is what "nothing notable about this row" should look like.
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final tint = color ?? scheme.onSurfaceVariant;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: tint.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        label,
        style: TextStyle(
          fontSize: 12,
          fontWeight: FontWeight.w600,
          color: tint,
        ),
      ),
    );
  }
}

/// The footer of a paged listing: which page, and the two ways off it.
class AdminPager extends StatelessWidget {
  const AdminPager({
    super.key,
    required this.page,
    required this.total,
    required this.limit,
    required this.onPrev,
    required this.onNext,
  });

  final int page;
  final int total;
  final int limit;
  final VoidCallback onPrev;
  final VoidCallback onNext;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    // One page of results needs no pager at all: showing a dead one on every short list is noise
    // that says "there is more" when there is not.
    if (total <= limit) return const SizedBox.shrink();

    final lastPage = (total + limit - 1) ~/ limit;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 12),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          AdaptiveButton.text(
            onPressed: page > 1 ? onPrev : null,
            child: Text(l10n.t('admin.prev')),
          ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 12),
            child: Text(
              l10n
                  .t('admin.pageOf')
                  .replaceAll('{page}', '$page')
                  .replaceAll('{pages}', '$lastPage'),
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ),
          AdaptiveButton.text(
            onPressed: page < lastPage ? onNext : null,
            child: Text(l10n.t('admin.next')),
          ),
        ],
      ),
    );
  }
}

/// A screen's primary action — "add user", "new invitation".
///
/// Declared rather than passed as a built widget, because WHERE it goes is a platform decision the
/// screen should not have to make twice: last on the bar on iOS, a floating button on Android.
///
/// AdaptiveScaffold does not enforce that — it draws whatever floatingActionButton it is handed on
/// either platform — so the first version of these screens, which passed a raw Material
/// FloatingActionButton through, put a Material floating button on an iPhone.
@immutable
class AdminAction {
  const AdminAction({
    required this.icon,
    required this.iosIcon,
    required this.label,
    required this.onPressed,
  });

  final IconData icon;
  final IconData iosIcon;
  final String label;
  final VoidCallback onPressed;
}

/// The body of a paged admin listing: loading, error, empty and rows, in one place.
///
/// Takes the page rather than a list so the pager and the emptiness check read the same numbers the
/// server sent — a screen that tracked "is empty" separately from "what page am I on" is how the
/// web panel briefly showed "no users" on page 2 of a list that had plenty.
class AdminListBody<T> extends StatelessWidget {
  const AdminListBody({
    super.key,
    required this.page,
    required this.loading,
    required this.error,
    required this.emptyLabel,
    required this.itemBuilder,
    required this.onRetry,
    required this.onPageChanged,
  });

  final AdminPage<T>? page;
  final bool loading;
  final bool error;
  final String emptyLabel;
  final Widget Function(BuildContext, T) itemBuilder;
  final VoidCallback onRetry;
  final void Function(int page) onPageChanged;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    if (error) {
      return ErrorView(message: l10n.t('admin.loadFailed'), onRetry: onRetry);
    }
    // Only on the FIRST load. A refresh keeps the rows on screen and lets the pull-to-refresh
    // spinner say what is happening, rather than blanking a list the reader was looking at.
    if (page == null) {
      return loading
          ? const Center(child: AdaptiveProgress())
          : const SizedBox.shrink();
    }

    final data = page!;
    return AdaptiveRefreshableScrollView(
      onRefresh: () async => onPageChanged(data.page),
      slivers: [
        if (data.items.isEmpty)
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 48),
              child: Center(
                child: Text(
                  emptyLabel,
                  style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                ),
              ),
            ),
          )
        else
          // No dividers, and vertical breathing room rather than rules between rows — the same
          // treatment the chat and channel lists get. A ruled list is the Material default, not
          // this app's.
          SliverPadding(
            padding: const EdgeInsets.symmetric(vertical: 4),
            sliver: SliverList.builder(
              itemCount: data.items.length,
              itemBuilder: (context, i) => itemBuilder(context, data.items[i]),
            ),
          ),
        SliverToBoxAdapter(
          child: AdminPager(
            page: data.page,
            total: data.total,
            limit: data.limit,
            onPrev: () => onPageChanged(data.page - 1),
            onNext: () => onPageChanged(data.page + 1),
          ),
        ),
      ],
    );
  }
}

/// Formats an ISO timestamp as a plain date, or a dash when there is not one.
///
/// Dates in the admin panel are for telling rows apart and spotting something old, never for
/// arithmetic — so a locale-independent short form is the honest rendering, and a malformed one
/// shows as a dash rather than throwing inside a list builder.
String adminDate(String? iso) {
  if (iso == null || iso.isEmpty) return '—';
  final parsed = DateTime.tryParse(iso);
  if (parsed == null) return '—';
  final local = parsed.toLocal();
  final m = local.month.toString().padLeft(2, '0');
  final d = local.day.toString().padLeft(2, '0');
  return '${local.year}-$m-$d';
}

/// A whole paged admin screen: chrome, pinned search, rows, pager, and the load/retry/refresh state
/// machine behind them.
///
/// Every list in the panel is an instance of this with a different [fetch] and [rowBuilder]. The
/// alternative — five screens each with its own copy of "query, page, loading, error, reload" — is
/// what the web panel has, and its five copies do not agree about when a search resets the page.
class AdminListScreen<T> extends StatefulWidget {
  const AdminListScreen({
    super.key,
    required this.title,
    required this.emptyLabel,
    required this.fetch,
    required this.rowBuilder,
    this.searchPlaceholder,
    this.primaryAction,
  });

  final String title;
  final String emptyLabel;

  /// Fetches one page. Called with the committed search text and a 1-based page number.
  final Future<AdminPage<T>> Function(String query, int page) fetch;

  /// Builds one row. `reload` re-fetches the CURRENT page — what an action calls once it has
  /// changed something, so the row redraws from the server rather than from a local guess.
  final Widget Function(BuildContext context, T item, VoidCallback reload)
  rowBuilder;

  /// Null hides the search field, for a listing there is nothing to search by.
  final String? searchPlaceholder;

  /// The screen's one creating action, if it has one. `reload` refreshes the list after it.
  final AdminAction Function(BuildContext context, VoidCallback reload)?
  primaryAction;

  @override
  State<AdminListScreen<T>> createState() => _AdminListScreenState<T>();
}

class _AdminListScreenState<T> extends State<AdminListScreen<T>> {
  final _search = TextEditingController();

  /// The text the CURRENT results were fetched with, which is not the text in the box: the field
  /// updates on every keystroke and this only when a request is actually made.
  String _query = '';
  int _page = 1;
  AdminPage<T>? _data;
  bool _loading = true;
  bool _error = false;

  /// Guards against an older response landing after a newer one and overwriting it — which is what
  /// happens when a search is retyped faster than the server answers.
  int _generation = 0;

  /// Waits for typing to stop before asking the server. [AdaptiveSearchField] reports every
  /// keystroke, and a request per keystroke is both wasteful and visibly jumpy.
  Timer? _debounce;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _search.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final generation = ++_generation;
    setState(() {
      _loading = true;
      _error = false;
    });
    try {
      final data = await widget.fetch(_query, _page);
      if (!mounted || generation != _generation) return;
      setState(() {
        _data = data;
        _loading = false;
      });
    } on Object {
      if (!mounted || generation != _generation) return;
      setState(() {
        _loading = false;
        // Only a FIRST load shows the error page. A failed refresh of a list already on screen
        // keeps the rows and lets the action that triggered it report the failure — replacing a
        // populated list with "load failed" loses the reader's place for a transient blip.
        _error = _data == null;
      });
    }
  }

  void _goToPage(int page) {
    if (page < 1) return;
    setState(() => _page = page);
    _load();
  }

  /// Reacts to typing. Clearing the field takes effect at once — the reader is asking for the full
  /// list back, and making them wait for a debounce reads as the clear button being broken.
  void _onSearchChanged(String value) {
    _debounce?.cancel();
    final text = value.trim();
    if (text == _query) return;
    if (text.isEmpty) {
      _applySearch(text);
      return;
    }
    _debounce = Timer(
      const Duration(milliseconds: 350),
      () => _applySearch(text),
    );
  }

  void _applySearch(String text) {
    if (!mounted || text == _query) return;
    setState(() {
      _query = text;
      // A new search is a new result set, so the page number it was found on means nothing. Not
      // resetting this is how a search from page 4 comes back empty and looks like "no matches".
      _page = 1;
    });
    _load();
  }

  @override
  Widget build(BuildContext context) {
    final ios = isCupertino(context);
    final placeholder = widget.searchPlaceholder;
    final action = widget.primaryAction?.call(context, _load);

    return AdaptiveScaffold(
      title: Text(widget.title),
      behindChrome: true,
      // The primary action goes where each platform puts it: last on the bar on iOS, floating on
      // Android. See AdminAction.
      trailing: [
        if (ios && action != null)
          GlassIconButton(
            icon: action.iosIcon,
            semanticLabel: action.label,
            onPressed: action.onPressed,
          ),
      ],
      floatingActionButton: ios || action == null
          ? null
          : GlassActionButton(
              icon: action.icon,
              semanticLabel: action.label,
              onPressed: action.onPressed,
            ),
      body: Builder(
        builder: (context) {
          final media = MediaQuery.of(context);
          // The field is pinned OUTSIDE the scroll view, and the list is told to start below it the
          // same way it is told to start below the bar: as top padding. Carrying it inside the list
          // instead — which is what these screens did first — means searching a long list starts
          // with scrolling back up to find the box that would have saved the scrolling.
          final feed = MediaQuery(
            data: media.copyWith(
              padding: media.padding.copyWith(
                top:
                    media.padding.top +
                    (placeholder != null ? PinnedSearchHeader.extent : 0),
              ),
            ),
            child: AdminListBody<T>(
              page: _data,
              loading: _loading,
              error: _error,
              emptyLabel: widget.emptyLabel,
              onRetry: _load,
              onPageChanged: _goToPage,
              itemBuilder: (context, item) =>
                  widget.rowBuilder(context, item, _load),
            ),
          );

          if (placeholder == null) return feed;

          return Stack(
            children: [
              Positioned.fill(child: feed),
              Positioned(
                top: media.padding.top,
                left: 0,
                right: 0,
                child: PinnedSearchHeader(
                  controller: _search,
                  placeholder: placeholder,
                  onChanged: _onSearchChanged,
                ),
              ),
            ],
          );
        },
      ),
    );
  }
}
