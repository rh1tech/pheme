import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';

import '../../core/snackbar.dart';
import '../../core/providers.dart';
import '../../l10n/app_localizations.dart';

/// The bar at the foot of a channel, where a chat has its message box.
///
/// Posting used to be a whole tab of its own — a form with a title field, a body field, an image
/// picker and a toggle, reached by leaving the messages behind. That is a page for composing a
/// document, and a channel post is a message. You write it where you read them.
///
/// A channel post does carry more than a chat message: it has a title, and it decides whether
/// readers may comment. Rather than putting three controls in the bar, the first line of what is
/// typed becomes the title and the rest the body — the same split the web makes — and the comment
/// switch lives behind the gear, where a per-message option belongs.
class ChannelComposer extends ConsumerStatefulWidget {
  const ChannelComposer({
    super.key,
    required this.channelId,
    required this.onSent,
  });

  final String channelId;
  final VoidCallback onSent;

  @override
  ConsumerState<ChannelComposer> createState() => _ChannelComposerState();
}

class _ChannelComposerState extends ConsumerState<ChannelComposer> {
  static const _maxImages = 10;

  final _text = TextEditingController();
  final _focus = FocusNode();
  final List<XFile> _images = [];
  bool _allowComments = true;
  bool _sending = false;

  @override
  void dispose() {
    _text.dispose();
    _focus.dispose();
    super.dispose();
  }

  bool get _canSend =>
      !_sending && (_text.text.trim().isNotEmpty || _images.isNotEmpty);

  /// The first line is the title, the rest is the body.
  ///
  /// A channel post has always had both, and the old form asked for them separately. Almost every
  /// post is one line, and asking for a title and then a body for one sentence is two empty boxes
  /// where a message should be. A post that genuinely has both still gets both — write a line,
  /// press return, keep going.
  (String, String) _split() {
    final text = _text.text.trim();
    final newline = text.indexOf('\n');
    if (newline < 0) return (text, '');
    return (
      text.substring(0, newline).trim(),
      text.substring(newline + 1).trim(),
    );
  }

  Future<void> _pickImages() async {
    final l10n = context.l10n;
    try {
      final picked = await ImagePicker().pickMultiImage();
      if (picked.isEmpty || !mounted) return;
      setState(() {
        final room = _maxImages - _images.length;
        _images.addAll(picked.take(room < 0 ? 0 : room));
      });
    } on Object catch (e) {
      if (mounted) notifyError(context, l10n.t('chat.photoFailed'), e);
    }
  }

  Future<void> _send() async {
    if (!_canSend) return;
    final l10n = context.l10n;
    final (title, body) = _split();
    setState(() => _sending = true);
    try {
      await ref
          .read(repositoryProvider)
          .notifyChannel(
            widget.channelId,
            title,
            body,
            imagePaths: _images.map((f) => f.path).toList(),
            allowComments: _allowComments,
          );
      if (!mounted) return;
      _text.clear();
      setState(() => _images.clear());
      widget.onSent();
    } on Object catch (e) {
      if (mounted) notifyError(context, l10n.t('channel.sendFailed'), e);
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l10n = context.l10n;

    return SafeArea(
      top: false,
      child: Container(
        decoration: BoxDecoration(
          color: theme.colorScheme.surface,
          border: Border(
            top: BorderSide(color: theme.dividerColor.withValues(alpha: 0.5)),
          ),
        ),
        padding: const EdgeInsets.fromLTRB(4, 6, 8, 6),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (_images.isNotEmpty)
              SizedBox(
                height: 64,
                child: ListView.separated(
                  scrollDirection: Axis.horizontal,
                  padding: const EdgeInsets.fromLTRB(8, 0, 8, 6),
                  itemCount: _images.length,
                  separatorBuilder: (_, _) => const SizedBox(width: 6),
                  itemBuilder: (context, i) => _Thumb(
                    file: _images[i],
                    onRemove: () => setState(() => _images.removeAt(i)),
                  ),
                ),
              ),
            Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                IconButton(
                  icon: const Icon(Icons.attach_file),
                  tooltip: l10n.t('chat.attachPhoto'),
                  onPressed: _sending ? null : _pickImages,
                ),
                // Per-message options, behind a gear as the web has it: whether this post may be
                // commented on is a property of the post, not a setting of the channel.
                IconButton(
                  icon: Icon(
                    Icons.settings_outlined,
                    color: _allowComments
                        ? null
                        : theme.colorScheme.onSurfaceVariant,
                  ),
                  tooltip: l10n.t('channel.allowComments'),
                  onPressed: _sending ? null : _showOptions,
                ),
                Expanded(
                  child: TextField(
                    controller: _text,
                    focusNode: _focus,
                    minLines: 1,
                    maxLines: 5,
                    textCapitalization: TextCapitalization.sentences,
                    onChanged: (_) => setState(() {}),
                    decoration: InputDecoration(
                      hintText: l10n.t('channel.writeMessage'),
                      border: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(20),
                      ),
                      contentPadding: const EdgeInsets.symmetric(
                        horizontal: 14,
                        vertical: 10,
                      ),
                      isDense: true,
                    ),
                  ),
                ),
                const SizedBox(width: 4),
                IconButton.filled(
                  // Explicit colours. The filled default resolves to primary on primaryContainer,
                  // which on this palette is a violet arrow on a violet disc — the button was there
                  // and could not be read.
                  style: IconButton.styleFrom(
                    backgroundColor: Theme.of(context).colorScheme.primary,
                    foregroundColor: Theme.of(context).colorScheme.onPrimary,
                    disabledBackgroundColor: Theme.of(
                      context,
                    ).colorScheme.surfaceContainerHighest,
                    disabledForegroundColor: Theme.of(
                      context,
                    ).colorScheme.onSurfaceVariant,
                  ),
                  icon: _sending
                      ? const SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.send),
                  tooltip: l10n.t('chat.send'),
                  onPressed: _canSend ? _send : null,
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  void _showOptions() {
    final l10n = context.l10n;
    showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: StatefulBuilder(
          builder: (context, setSheetState) => SwitchListTile(
            title: Text(l10n.t('channel.allowComments')),
            subtitle: Text(l10n.t('channel.allowCommentsHint')),
            value: _allowComments,
            onChanged: (v) {
              setSheetState(() {});
              setState(() => _allowComments = v);
            },
          ),
        ),
      ),
    );
  }
}

class _Thumb extends StatelessWidget {
  const _Thumb({required this.file, required this.onRemove});

  final XFile file;
  final VoidCallback onRemove;

  @override
  Widget build(BuildContext context) {
    return Stack(
      children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(8),
          child: Image.file(
            File(file.path),
            width: 64,
            height: 64,
            fit: BoxFit.cover,
            errorBuilder: (_, _, _) => Container(
              width: 64,
              height: 64,
              color: Theme.of(context).colorScheme.surfaceContainerHighest,
              child: const Icon(Icons.image_outlined),
            ),
          ),
        ),
        Positioned(
          right: 0,
          top: 0,
          child: InkWell(
            onTap: onRemove,
            child: Container(
              decoration: const BoxDecoration(
                color: Colors.black54,
                shape: BoxShape.circle,
              ),
              padding: const EdgeInsets.all(2),
              child: const Icon(Icons.close, size: 14, color: Colors.white),
            ),
          ),
        ),
      ],
    );
  }
}
