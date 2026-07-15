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
class _CallLayer extends ConsumerWidget {
  const _CallLayer();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final call = ref.watch(callProvider);
    if (call == null) return const SizedBox.shrink();
    return call.isIncomingRing
        ? _IncomingCall(call: call)
        : Positioned(
            top: MediaQuery.paddingOf(context).top + 8,
            left: 16,
            right: 16,
            child: _CallBar(call: call),
          );
  }
}

/// Somebody is calling. A full-screen sheet, because a ringing phone is not a notification.
class _IncomingCall extends ConsumerWidget {
  const _IncomingCall({required this.call});

  final CallState call;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    return Positioned.fill(
      child: ColoredBox(
        color: theme.colorScheme.surface,
        child: SafeArea(
          child: Column(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Padding(
                padding: const EdgeInsets.only(top: 64),
                child: Column(
                  children: [
                    Text(
                      l10n.t('call.incoming'),
                      style: theme.textTheme.headlineSmall?.copyWith(
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const SizedBox(height: 8),
                    Text(
                      l10n.t('call.incomingBody'),
                      style: theme.textTheme.bodyMedium?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                    ),
                  ],
                ),
              ),

              Padding(
                padding: const EdgeInsets.only(bottom: 64),
                child: Row(
                  mainAxisAlignment: MainAxisAlignment.spaceEvenly,
                  children: [
                    _RoundButton(
                      icon: Icons.call_end,
                      color: theme.colorScheme.error,
                      label: l10n.t('call.decline'),
                      onPressed: () =>
                          ref.read(callProvider.notifier).decline(),
                    ),
                    _RoundButton(
                      icon: Icons.call,
                      color: const Color(0xFF40C057),
                      label: l10n.t('call.answer'),
                      // The invite may not have been read yet — on iOS the phone rings before the app
                      // is allowed to go and fetch it. Answering waits for it rather than failing, so
                      // the button stays live; it is the engine that blocks.
                      onPressed: () => ref.read(callProvider.notifier).answer(),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// A call in progress. A floating pill rather than a full screen: the call is not the only thing the
/// user may want to be doing.
class _CallBar extends ConsumerWidget {
  const _CallBar({required this.call});

  final CallState call;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final ended = call.status == CallStatus.ended;
    final connected = call.status == CallStatus.connected;

    return Material(
      elevation: 8,
      borderRadius: BorderRadius.circular(999),
      color: theme.colorScheme.surfaceContainerHigh,
      child: Container(
        padding: const EdgeInsets.fromLTRB(16, 8, 8, 8),
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(999),
          border: Border.all(
            color: connected
                ? const Color(0xFF40C057)
                : theme.dividerColor.withValues(alpha: 0.4),
          ),
        ),
        child: Row(
          children: [
            Expanded(
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

            if (ended)
              TextButton(
                onPressed: () => ref.read(callProvider.notifier).dismiss(),
                child: Text(l10n.t('call.dismiss')),
              )
            else ...[
              IconButton(
                onPressed: () =>
                    ref.read(callProvider.notifier).setMuted(!call.muted),
                icon: Icon(call.muted ? Icons.mic_off : Icons.mic),
                color: call.muted ? theme.colorScheme.error : null,
                tooltip: l10n.t(call.muted ? 'call.unmute' : 'call.mute'),
              ),
              IconButton(
                onPressed: () => ref
                    .read(callProvider.notifier)
                    .setRoute(
                      call.route == AudioRoute.speaker
                          ? AudioRoute.earpiece
                          : AudioRoute.speaker,
                    ),
                icon: Icon(
                  call.route == AudioRoute.speaker
                      ? Icons.volume_up
                      : Icons.volume_down,
                ),
                tooltip: l10n.t('call.speaker'),
              ),
              IconButton.filled(
                onPressed: () => ref.read(callProvider.notifier).hangUp(),
                icon: const Icon(Icons.call_end),
                style: IconButton.styleFrom(
                  backgroundColor: theme.colorScheme.error,
                  foregroundColor: theme.colorScheme.onError,
                ),
                tooltip: l10n.t('call.hangUp'),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _RoundButton extends StatelessWidget {
  const _RoundButton({
    required this.icon,
    required this.color,
    required this.label,
    required this.onPressed,
  });

  final IconData icon;
  final Color color;
  final String label;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        SizedBox(
          width: 64,
          height: 64,
          child: IconButton.filled(
            onPressed: onPressed,
            icon: Icon(icon, size: 28),
            style: IconButton.styleFrom(
              backgroundColor: color,
              foregroundColor: Colors.white,
            ),
          ),
        ),
        const SizedBox(height: 8),
        Text(label, style: Theme.of(context).textTheme.bodySmall),
      ],
    );
  }
}
