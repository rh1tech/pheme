// The two channel actions that are a single decision each: sharing it, and whether this device
// should be woken by it.
//
// Sheets rather than screens, and deliberately not the tall management sheet the subscriber and key
// lists use. Each of these is one thing to look at and at most one button to press — a full screen
// for that is a journey where a glance would do, and pushing a route to read a QR code means finding
// your way back afterwards.

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../core/providers.dart';
import '../core/snackbar.dart';
import '../l10n/app_localizations.dart';
import '../models/models.dart';
import '../widgets/adaptive/adaptive.dart';
import 'channel_sheets.dart';

/// The channel's join reference, as a code to scan and a string to copy.
Future<void> showChannelShareSheet(BuildContext context, Channel channel) {
  return showChannelSheet<void>(
    context,
    title: AppLocalizations.of(context).t('channel.shareTitle'),
    child: _ShareBody(channel: channel),
  );
}

class _ShareBody extends ConsumerWidget {
  const _ShareBody({required this.channel});

  final Channel channel;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = context.l10n;
    final scheme = Theme.of(context).colorScheme;
    final joinRef = channel.joinRef;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          l10n.t('channel.shareDescription'),
          style: TextStyle(color: scheme.onSurfaceVariant, fontSize: 13),
        ),
        const SizedBox(height: 20),
        Center(
          // White plate whatever the theme: a QR is read by a camera, and inverted codes are a coin
          // toss across scanners.
          child: Container(
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              color: Colors.white,
              borderRadius: BorderRadius.circular(12),
            ),
            child: QrImageView(
              data: joinRef,
              size: 190,
              backgroundColor: Colors.white,
            ),
          ),
        ),
        const SizedBox(height: 20),
        Text(
          l10n.t('channel.shareRef'),
          style: TextStyle(fontSize: 12, color: scheme.onSurfaceVariant),
        ),
        const SizedBox(height: 4),
        Row(
          children: [
            Expanded(
              child: SelectableText(
                joinRef,
                style: const TextStyle(
                  fontFamily: 'monospace',
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            AdaptiveIconButton(
              icon: isCupertino(context)
                  ? CupertinoIcons.doc_on_doc
                  : Icons.copy,
              semanticLabel: l10n.t('common.copy'),
              onPressed: () async {
                await Clipboard.setData(ClipboardData(text: joinRef));
                if (context.mounted) {
                  notifySuccess(context, l10n.t('channel.refCopied'));
                }
              },
            ),
          ],
        ),
      ],
    );
  }
}

/// Whether this device is woken by the channel's posts.
Future<void> showChannelNotificationsSheet(
  BuildContext context,
  String channelId,
) {
  return showChannelSheet<void>(
    context,
    title: AppLocalizations.of(context).t('channel.subscribeTitle'),
    child: _NotificationsBody(channelId: channelId),
  );
}

class _NotificationsBody extends ConsumerStatefulWidget {
  const _NotificationsBody({required this.channelId});

  final String channelId;

  @override
  ConsumerState<_NotificationsBody> createState() => _NotificationsBodyState();
}

class _NotificationsBodyState extends ConsumerState<_NotificationsBody> {
  SubscriptionStatus _status = SubscriptionStatus.none;
  bool _busy = false;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final deviceId = ref.read(deviceControllerProvider);
    if (deviceId == null) {
      if (mounted) setState(() => _loading = false);
      return;
    }
    try {
      final status = await ref
          .read(repositoryProvider)
          .channelSubscription(widget.channelId, deviceId);
      if (mounted) setState(() => _status = status);
    } catch (_) {
      // Stays "none". Subscribing self-heals it, and a status we could not read is not worth an
      // error on a sheet whose whole content is one button.
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _subscribe() async {
    final l10n = context.l10n;
    final deviceId = ref.read(deviceControllerProvider);
    if (deviceId == null) {
      notifyError(context, l10n.t('channel.subscribeNeedsDevice'));
      return;
    }
    setState(() => _busy = true);
    try {
      await ref.read(repositoryProvider).subscribe(widget.channelId, deviceId);
      await _load();
      if (mounted) notifySuccess(context, l10n.t('channel.subscribed'));
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('channel.subscribeFailed'), e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _unsubscribe() async {
    final l10n = context.l10n;
    final deviceId = ref.read(deviceControllerProvider);
    if (deviceId == null) return;
    setState(() => _busy = true);
    try {
      await ref
          .read(repositoryProvider)
          .unsubscribe(widget.channelId, deviceId);
      if (mounted) {
        setState(() => _status = SubscriptionStatus.none);
        notifySuccess(context, l10n.t('channel.unsubscribed'));
      }
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('channel.unsubscribeFailed'), e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final scheme = Theme.of(context).colorScheme;
    final registered = ref.watch(deviceControllerProvider) != null;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          l10n.t('channel.subscribeDescription'),
          style: TextStyle(color: scheme.onSurfaceVariant, fontSize: 13),
        ),
        if (_status == SubscriptionStatus.pending) ...[
          const SizedBox(height: 10),
          Text(
            l10n.t('channel.subscriptionPending'),
            style: TextStyle(color: scheme.secondary, fontSize: 13),
          ),
        ],
        // Said before the button rather than after it fails: a device with no push registration
        // cannot be subscribed, and offering the button anyway is a refusal waiting to happen.
        if (!registered) ...[
          const SizedBox(height: 10),
          Text(
            l10n.t('channel.subscribeNeedsDevice'),
            style: TextStyle(color: scheme.error, fontSize: 12),
          ),
        ],
        const SizedBox(height: 20),
        if (_loading)
          const Center(child: AdaptiveProgress(size: 20))
        else if (_status == SubscriptionStatus.none)
          AdaptiveButton.filled(
            onPressed: (_busy || !registered) ? null : _subscribe,
            child: _busy
                ? const AdaptiveProgress(size: 18)
                : Text(l10n.t('channel.subscribe')),
          )
        else
          AdaptiveButton.outlined(
            isDestructive: true,
            onPressed: _busy ? null : _unsubscribe,
            child: _busy
                ? const AdaptiveProgress(size: 18)
                : Text(l10n.t('channel.unsubscribe')),
          ),
      ],
    );
  }
}
