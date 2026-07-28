// The call screen: who you are talking to, and the few things you can do about it.
//
// Modelled on the system dialer rather than on the rest of the app. A call is its own context — it
// outranks whatever was on screen, it is usually held to the face, and it is answered by people who
// are not looking carefully. So: dark whatever the app's theme, one large name, and controls that
// are big round targets with words under them rather than glyphs you have to interpret.
//
// Dark deliberately, and not from the colour scheme. Every dialer on both platforms is dark, in
// light mode too, because the screen is against a cheek and because a call is a mode rather than a
// page. Following the app's theme here would make the one screen that should feel like the system's
// feel like a form.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../chat/chat_providers.dart';
import '../chat/conversation_title.dart';
import '../chat/widgets/conversation_avatar.dart';
import '../core/providers.dart';
import '../l10n/app_localizations.dart';
import '../models/chat_models.dart';
import 'call_controller.dart';
import 'call_state.dart';

/// The dialer's palette. Fixed, for the reasons in the header.
abstract final class CallColors {
  static const backdropTop = Color(0xFF1A1722);
  static const backdrop = Color(0xFF0B0A0F);
  static const label = Colors.white;
  static const mutedLabel = Color(0xB3FFFFFF);
  static const control = Color(0x26FFFFFF);
  static const controlActive = Colors.white;
  static const end = Color(0xFFE5484D);
  static const answer = Color(0xFF30C05A);
}

/// Who the call is with, resolved from the conversation it belongs to.
///
/// The old call bar showed a status and a timer and NOTHING ELSE — no name, no face. It is the one
/// screen where "who is this" is the whole question, and it was the one screen that did not answer
/// it.
class CallParty {
  const CallParty({required this.name, this.avatarUrl, required this.id});

  final String name;
  final String? avatarUrl;
  final String id;
}

final callPartyProvider = Provider.family<CallParty?, String>((
  ref,
  conversationId,
) {
  final conversation = ref.watch(conversationProvider(conversationId)).value;
  if (conversation == null) return null;
  final myUserId = ref.watch(myUserIdProvider);
  final other = conversation.otherMember(myUserId);

  return CallParty(
    id: other?.userId ?? conversation.id,
    name: conversationTitleOf(conversation, myUserId),
    avatarUrl: conversationAvatarUrl(
      isGroup: conversation.isGroup,
      groupAvatarId: conversation.avatarId,
      otherAvatarId: other?.user.avatarId,
      toUrl: ref.read(repositoryProvider).imageUrl,
    ),
  );
});

/// The name without a localisation lookup, for the provider above — a call's party is a person, and
/// the "New chat" fallback that [conversationTitle] uses would be nonsense on a ringing phone.
String conversationTitleOf(Conversation conversation, String myUserId) {
  if (conversation.isGroup) return conversation.title ?? '';
  final other = conversation.otherMember(myUserId);
  return other == null ? '' : userLabel(other.user);
}

/// A call in progress, full screen.
class CallScreen extends ConsumerWidget {
  const CallScreen({super.key, required this.call, required this.onMinimise});

  final CallState call;

  /// Folds the screen down to the floating bar, so the app is reachable mid-call. iOS keeps the
  /// call in a pill at the top of the screen for the same reason: a call is not a reason to lose
  /// the ability to look something up.
  final VoidCallback onMinimise;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final party = ref.watch(callPartyProvider(call.conversationId));
    final controller = ref.read(callProvider.notifier);
    final ended = call.status == CallStatus.ended;

    return _CallBackdrop(
      child: Column(
        children: [
          _MinimiseBar(onPressed: onMinimise, label: l10n.t('call.minimise')),
          const SizedBox(height: 8),
          _CallHeader(
            party: party,
            status: call.status == CallStatus.connected
                ? call.elapsed
                : l10n.t(call.statusKey),
          ),
          const Spacer(),
          if (ended)
            _EndedActions(
              label: l10n.t('call.dismiss'),
              onDismiss: controller.dismiss,
            )
          else
            _ActiveControls(call: call, controller: controller, l10n: l10n),
          const SizedBox(height: 40),
        ],
      ),
    );
  }
}

/// Somebody is calling. The same screen, with the two buttons that matter.
class IncomingCallScreen extends ConsumerWidget {
  const IncomingCallScreen({super.key, required this.call});

  final CallState call;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final party = ref.watch(callPartyProvider(call.conversationId));
    final controller = ref.read(callProvider.notifier);

    return _CallBackdrop(
      child: Column(
        children: [
          const SizedBox(height: 24),
          _CallHeader(party: party, status: l10n.t('call.incoming')),
          const Spacer(),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 44),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                _CallButton(
                  icon: Icons.call_end,
                  label: l10n.t('call.decline'),
                  background: CallColors.end,
                  foreground: Colors.white,
                  size: 74,
                  onPressed: controller.decline,
                ),
                _CallButton(
                  icon: Icons.call,
                  label: l10n.t('call.answer'),
                  background: CallColors.answer,
                  foreground: Colors.white,
                  size: 74,
                  // The invite may not have arrived yet — iOS makes the phone ring before the app is
                  // allowed to fetch it. The button stays live and the engine waits; see
                  // CallState.inviteReady.
                  onPressed: controller.answer,
                ),
              ],
            ),
          ),
          const SizedBox(height: 56),
        ],
      ),
    );
  }
}

/// The dark gradient every dialer sits on, plus the safe area — and the focus.
///
/// TAKING FOCUS is the load-bearing part, not decoration. The call surface is an overlay above the
/// router rather than a route, which is what lets a call outlive the screen it was placed from; the
/// cost is that whatever the user was typing in KEEPS its focus underneath, so the keyboard stands
/// over the dialer and hides every control — on an incoming call, over Answer and Decline.
///
/// Asking the old focus to go away is not enough on its own: the field underneath is still there,
/// still focusable, and anything that returns focus to it raises the keyboard again over a screen
/// that has no text on it. Somewhere in this tree has to actually HOLD the focus, and this is the
/// widget every call screen is built on.
class _CallBackdrop extends StatefulWidget {
  const _CallBackdrop({required this.child});

  final Widget child;

  @override
  State<_CallBackdrop> createState() => _CallBackdropState();
}

class _CallBackdropState extends State<_CallBackdrop> {
  @override
  void initState() {
    super.initState();
    // Belt as well as braces. The autofocus below moves focus here, which dismisses the keyboard in
    // the ordinary case; this asks the platform directly, for the case where the keyboard was raised
    // by something that is no longer the primary focus at all.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      FocusManager.instance.primaryFocus?.unfocus();
      SystemChannels.textInput.invokeMethod<void>('TextInput.hide');
    });
  }

  @override
  Widget build(BuildContext context) {
    return Focus(
      autofocus: true,
      // Not a traversal stop for anything else: there is nothing on a call screen to tab between.
      // This node exists to be the thing that HOLDS focus, so nothing underneath can.
      child: _backdrop(),
    );
  }

  Widget _backdrop() {
    return DecoratedBox(
      decoration: const BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topCenter,
          end: Alignment.bottomCenter,
          colors: [CallColors.backdropTop, CallColors.backdrop],
        ),
      ),
      // Its own light-on-dark defaults, since this screen does not take the app's.
      child: DefaultTextStyle(
        style: const TextStyle(
          color: CallColors.label,
          decoration: TextDecoration.none,
        ),
        child: SafeArea(child: widget.child),
      ),
    );
  }
}

/// The avatar, the status line and the name — the reference layout, in that reading order: what is
/// happening, then who with.
class _CallHeader extends StatelessWidget {
  const _CallHeader({required this.party, required this.status});

  final CallParty? party;
  final String status;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 24),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          if (party != null)
            ConversationAvatar(
              id: party!.id,
              label: party!.name,
              size: 72,
              imageUrl: party!.avatarUrl,
            ),
          if (party != null) const SizedBox(width: 16),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  status,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    fontSize: 19,
                    color: CallColors.mutedLabel,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  // A call placed before the conversation has loaded has no name yet. An empty line
                  // that fills in a moment later is better than a placeholder that has to be read
                  // and discarded.
                  party?.name ?? '',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    fontSize: 30,
                    fontWeight: FontWeight.w600,
                    letterSpacing: -0.5,
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _ActiveControls extends StatelessWidget {
  const _ActiveControls({
    required this.call,
    required this.controller,
    required this.l10n,
  });

  final CallState call;
  final CallController controller;
  final AppLocalizations l10n;

  @override
  Widget build(BuildContext context) {
    final onSpeaker = call.route == AudioRoute.speaker;

    // Three controls, one row, End in the middle — the dialer's shape, without inventing the
    // buttons it has and this app does not. There is no keypad on an encrypted voice call, no video
    // to switch to, and nothing behind a "more".
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 32),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceBetween,
        children: [
          _CallButton(
            icon: call.muted ? Icons.mic_off : Icons.mic,
            label: l10n.t(call.muted ? 'call.unmute' : 'call.mute'),
            // An engaged toggle inverts, as it does on both platforms: the filled state is the
            // unusual one, so it should be the one that stands out.
            background: call.muted
                ? CallColors.controlActive
                : CallColors.control,
            foreground: call.muted ? Colors.black : Colors.white,
            onPressed: () => controller.setMuted(!call.muted),
          ),
          _CallButton(
            icon: Icons.call_end,
            label: l10n.t('call.hangUp'),
            background: CallColors.end,
            foreground: Colors.white,
            size: 74,
            onPressed: controller.hangUp,
          ),
          _CallButton(
            icon: onSpeaker ? Icons.volume_up : Icons.volume_down,
            label: l10n.t('call.speaker'),
            background: onSpeaker
                ? CallColors.controlActive
                : CallColors.control,
            foreground: onSpeaker ? Colors.black : Colors.white,
            onPressed: () => controller.setRoute(
              onSpeaker ? AudioRoute.earpiece : AudioRoute.speaker,
            ),
          ),
        ],
      ),
    );
  }
}

class _EndedActions extends StatelessWidget {
  const _EndedActions({required this.label, required this.onDismiss});

  final String label;
  final VoidCallback onDismiss;

  @override
  Widget build(BuildContext context) {
    return _CallButton(
      icon: Icons.close,
      label: label,
      background: CallColors.control,
      foreground: Colors.white,
      onPressed: onDismiss,
    );
  }
}

/// A round control with its name underneath — the dialer's button.
///
/// The label is not decoration. Half of these are toggles whose glyph alone does not say whether it
/// is on, and the other half are irreversible.
class _CallButton extends StatefulWidget {
  const _CallButton({
    required this.icon,
    required this.label,
    required this.background,
    required this.foreground,
    required this.onPressed,
    this.size = 68,
  });

  final IconData icon;
  final String label;
  final Color background;
  final Color foreground;
  final VoidCallback onPressed;
  final double size;

  @override
  State<_CallButton> createState() => _CallButtonState();
}

class _CallButtonState extends State<_CallButton> {
  bool _down = false;

  void _set(bool down) {
    if (_down != down) setState(() => _down = down);
  }

  @override
  Widget build(BuildContext context) {
    return Semantics(
      button: true,
      label: widget.label,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTapDown: (_) => _set(true),
        onTapUp: (_) => _set(false),
        onTapCancel: () => _set(false),
        onTap: widget.onPressed,
        child: ExcludeSemantics(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              AnimatedScale(
                scale: _down ? 0.92 : 1,
                duration: const Duration(milliseconds: 90),
                curve: Curves.easeOut,
                child: AnimatedContainer(
                  duration: const Duration(milliseconds: 160),
                  width: widget.size,
                  height: widget.size,
                  decoration: BoxDecoration(
                    color: widget.background,
                    shape: BoxShape.circle,
                  ),
                  child: Icon(
                    widget.icon,
                    size: widget.size * 0.42,
                    color: widget.foreground,
                  ),
                ),
              ),
              const SizedBox(height: 9),
              SizedBox(
                width: 96,
                child: Text(
                  widget.label,
                  textAlign: TextAlign.center,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    fontSize: 13,
                    color: CallColors.mutedLabel,
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// The handle that folds the call away. A chevron at the top of the screen, where every sheet in
/// both platforms puts the same gesture.
class _MinimiseBar extends StatelessWidget {
  const _MinimiseBar({required this.onPressed, required this.label});

  final VoidCallback onPressed;
  final String label;

  @override
  Widget build(BuildContext context) {
    return Align(
      alignment: Alignment.centerLeft,
      child: Semantics(
        button: true,
        label: label,
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: onPressed,
          child: const Padding(
            padding: EdgeInsets.fromLTRB(16, 12, 24, 12),
            child: Icon(
              Icons.keyboard_arrow_down,
              color: CallColors.mutedLabel,
              size: 30,
            ),
          ),
        ),
      ),
    );
  }
}
