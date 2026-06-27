import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/format.dart';
import '../core/providers.dart';
import '../l10n/app_localizations.dart';
import '../models/models.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/error_view.dart';
import '../widgets/message_carousel.dart';

/// Full message view: all images in a carousel (Instagram-style) followed by the
/// title, timestamp and body. Reached by tapping a message or a notification.
class MessagePage extends ConsumerStatefulWidget {
  const MessagePage({
    super.key,
    required this.channelId,
    required this.messageId,
  });

  final String channelId;
  final String messageId;

  @override
  ConsumerState<MessagePage> createState() => _MessagePageState();
}

class _MessagePageState extends ConsumerState<MessagePage> {
  Message? _message;
  bool _loading = true;
  bool _error = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = false;
    });
    try {
      final message = await ref
          .read(repositoryProvider)
          .getMessage(widget.channelId, widget.messageId);
      if (!mounted) return;
      setState(() {
        _message = message;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = true;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return AdaptiveScaffold(
      title: Text(l10n.t('channel.messageView')),
      body: _body(context, l10n),
    );
  }

  Widget _body(BuildContext context, AppLocalizations l10n) {
    if (_loading) {
      return const Center(child: AdaptiveProgress());
    }
    final message = _message;
    if (_error || message == null) {
      return ErrorView(
        message: l10n.t('channel.messageNotFound'),
        onRetry: _load,
      );
    }
    final scheme = Theme.of(context).colorScheme;
    return ListView(
      children: [
        if (message.images.isNotEmpty) MessageCarousel(images: message.images),
        Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                message.title.isEmpty
                    ? l10n.t('channel.noTitle')
                    : message.title,
                style: const TextStyle(
                  fontWeight: FontWeight.w600,
                  fontSize: 18,
                ),
              ),
              const SizedBox(height: 4),
              Text(
                formatDateTime(message.createdAt),
                style: TextStyle(fontSize: 12, color: scheme.onSurfaceVariant),
              ),
              if (message.body.isNotEmpty) ...[
                const SizedBox(height: 12),
                Text(message.body, style: const TextStyle(fontSize: 15)),
              ],
            ],
          ),
        ),
      ],
    );
  }
}
