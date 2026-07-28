// The in-app call surface: an incoming-call sheet, and a floating bar for a call in progress.
//
// Mounted above the routes so it survives navigation — you can walk back to the conversation list
// mid-call and the bar comes with you, which is what the web's fixed-position pill does and what every
// phone does natively.
//
// This is the FOREGROUND surface. When the app is asleep the platform's own call UI rings instead
// (CallKit on iOS, a full-screen intent on Android) — see call_service.dart. Both drive the same
// CallController, so there is one call and one place it can be answered from.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../l10n/app_localizations.dart';
import 'call_screen.dart';
import '../widgets/glass/glass.dart';
import 'call_controller.dart';
import 'call_state.dart';

/// Wraps the app so a call can be shown over whatever is on screen.
class CallOverlay extends ConsumerWidget {
  const CallOverlay({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Only whether a call EXISTS, so the Overlay is added when one starts and removed when it ends.
    // The call's changing DETAILS (mute, route, status) are watched inside _CallLayer, not here.
    final hasCall = ref.watch(callProvider.select((c) => c != null));

    return Stack(
      children: [
        child,
        // The call UI is a sibling of the router's Navigator, so it has no Overlay of its own — and
        // the Material buttons inside it (tooltips, ink) need one. Give it a private Overlay, or a
        // tap on a call control throws "No Overlay widget found" and no call UI shows at all.
        //
        // The Overlay's entry is built ONCE, so it must not capture the call state — a static snapshot
        // is why mute and speaker stopped doing anything: they changed the state, but the frozen entry
        // never rebuilt. _CallLayer watches the call itself, so it rebuilds on every change.
        if (hasCall)
          Positioned.fill(
            child: Overlay(
              initialEntries: [
                OverlayEntry(builder: (_) => const _CallLayer()),
              ],
            ),
          ),
      ],
    );
  }
}

/// The live call surface, WATCHING the call so it rebuilds as mute, route and status change. It lives
/// inside the Overlay's entry, which is built once — so the reactivity has to be here, not above.
///
/// Two shapes, and the user chooses between them: the full dialer, and a floating bar that keeps the
/// call alive while the app is used for something else. A ringing phone is never the bar — an
/// incoming call is not a thing to be quietly noted while you carry on scrolling.
class _CallLayer extends ConsumerStatefulWidget {
  const _CallLayer();

  @override
  ConsumerState<_CallLayer> createState() => _CallLayerState();
}

class _CallLayerState extends ConsumerState<_CallLayer> {
  /// Starts open, because a call you just placed or answered is what you are doing. Folding it away
  /// is a decision, and it survives until the call ends.
  bool _expanded = true;

  /// The id the [_expanded] flag belongs to, so the NEXT call opens full screen rather than
  /// inheriting the last one's folded state.
  String? _callId;

  /// Whether the keyboard has already been put away for the call surface currently on screen.
  bool _keyboardDismissed = false;

  /// Takes the keyboard down when the call goes full screen.
  ///
  /// The call surface is an OVERLAY above the router, not a route — that is what lets a call
  /// survive navigation — but it means nothing takes focus away from whatever the user was typing
  /// in. So a call arriving mid-message left the keyboard standing over the dialer, covering the
  /// buttons: on an incoming call, over Answer and Decline. Pushing a route would have dismissed it
  /// for free; keeping the overlay costs this one line.
  ///
  /// After the frame, because unfocusing during build is not allowed. Once per surface, tracked by
  /// [_keyboardDismissed], so it cannot fight a user who deliberately focuses something later —
  /// though there is nothing on these screens to focus.
  void _dismissKeyboard() {
    if (_keyboardDismissed) return;
    _keyboardDismissed = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      FocusManager.instance.primaryFocus?.unfocus();
    });
  }

  @override
  Widget build(BuildContext context) {
    final call = ref.watch(callProvider);
    if (call == null) return const SizedBox.shrink();

    if (_callId != call.callId) {
      _callId = call.callId;
      _expanded = true;
      _keyboardDismissed = false;
    }

    if (call.isIncomingRing) {
      _dismissKeyboard();
      return Positioned.fill(child: IncomingCallScreen(call: call));
    }

    if (_expanded) {
      _dismissKeyboard();
      return Positioned.fill(
        child: CallScreen(
          call: call,
          onMinimise: () => setState(() => _expanded = false),
        ),
      );
    }

    // Folded: over the top bar rather than beside it, because a live call outranks whatever screen
    // it is covering, and the two are the same shape and the same material — so it reads as the bar
    // being replaced for the duration.
    return Positioned(
      top: MediaQuery.paddingOf(context).top + GlassMetrics.gap,
      left: GlassMetrics.gutter,
      right: GlassMetrics.gutter,
      child: _CallBar(
        call: call,
        // Re-arm the dismissal: the bar leaves the app usable, so the user may well have been
        // typing again by the time they bring the dialer back.
        onExpand: () => setState(() {
          _expanded = true;
          _keyboardDismissed = false;
        }),
      ),
    );
  }
}

/// A call in progress. A floating pill rather than a full screen: the call is not the only thing the
/// user may want to be doing.
class _CallBar extends ConsumerWidget {
  const _CallBar({required this.call, required this.onExpand});

  final CallState call;

  /// Tapping the bar — anywhere but its controls — brings the dialer back.
  final VoidCallback onExpand;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final ended = call.status == CallStatus.ended;
    final connected = call.status == CallStatus.connected;

    // The same glass the top bar is made of, so a call in progress reads as part of the app's
    // chrome rather than as a Material card that has landed on top of it. The green edge while
    // connected is the one thing that overrides the hairline: it is the only signal that the call
    // is actually up.
    return GlassSurface(
      floating: true,
      borderRadius: BorderRadius.circular(999),
      border: connected
          ? Border.all(color: const Color(0xFF40C057), width: 1)
          : null,
      padding: const EdgeInsets.fromLTRB(16, 6, 6, 6),
      child: Row(
        children: [
          Expanded(
            child: GestureDetector(
              behavior: HitTestBehavior.opaque,
              onTap: onExpand,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    connected ? call.elapsed : l10n.t(call.statusKey),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: theme.textTheme.bodyMedium?.copyWith(
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  if (connected)
                    Text(
                      l10n.t(call.muted ? 'call.muted' : 'call.encrypted'),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                    ),
                ],
              ),
            ),
          ),

          if (ended)
            TextButton(
              onPressed: () => ref.read(callProvider.notifier).dismiss(),
              child: Text(l10n.t('call.dismiss')),
            )
          else ...[
            GlassComposerGlyph(
              icon: call.muted ? Icons.mic_off : Icons.mic,
              semanticLabel: l10n.t(call.muted ? 'call.unmute' : 'call.mute'),
              muted: call.muted,
              onPressed: () =>
                  ref.read(callProvider.notifier).setMuted(!call.muted),
            ),
            GlassComposerGlyph(
              icon: call.route == AudioRoute.speaker
                  ? Icons.volume_up
                  : Icons.volume_down,
              semanticLabel: l10n.t('call.speaker'),
              onPressed: () => ref
                  .read(callProvider.notifier)
                  .setRoute(
                    call.route == AudioRoute.speaker
                        ? AudioRoute.earpiece
                        : AudioRoute.speaker,
                  ),
            ),
            const SizedBox(width: 2),
            _HangUpButton(onPressed: ref.read(callProvider.notifier).hangUp),
          ],
        ],
      ),
    );
  }
}

/// End the call: the same disc as the composer's send button, in the same place on the bar, in the
/// one colour the app reserves for "this is the destructive one".
class _HangUpButton extends StatelessWidget {
  const _HangUpButton({required this.onPressed});

  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    return Semantics(
      button: true,
      label: AppLocalizations.of(context).t('call.hangUp'),
      child: GestureDetector(
        onTap: onPressed,
        child: Container(
          width: 38,
          height: 38,
          decoration: BoxDecoration(
            color: scheme.error,
            shape: BoxShape.circle,
          ),
          child: Icon(Icons.call_end, size: 20, color: scheme.onError),
        ),
      ),
    );
  }
}
