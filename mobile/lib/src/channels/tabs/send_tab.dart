import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';

import '../../core/providers.dart';
import '../../core/snackbar.dart';
import '../../l10n/app_localizations.dart';

/// Keep these in sync with the server limits (api/internal/channel/notify_input.go).
const _maxImages = 10;
const _maxImageBytes = 10 * 1024 * 1024;

/// Lets a channel owner send a notification from the app (uses their JWT via
/// POST /v1/channels/{id}/notify — the one bridge into the ingest path).
class SendTab extends ConsumerStatefulWidget {
  const SendTab({super.key, required this.channelId});

  final String channelId;

  @override
  ConsumerState<SendTab> createState() => _SendTabState();
}

class _SendTabState extends ConsumerState<SendTab> {
  final _title = TextEditingController();
  final _body = TextEditingController();
  final _picker = ImagePicker();
  final List<XFile> _images = [];
  bool _sending = false;

  @override
  void dispose() {
    _title.dispose();
    _body.dispose();
    super.dispose();
  }

  bool get _canSend =>
      _title.text.trim().isNotEmpty ||
      _body.text.trim().isNotEmpty ||
      _images.isNotEmpty;

  Future<void> _pickImages() async {
    final l10n = context.l10n;
    final picked = await _picker.pickMultiImage();
    if (picked.isEmpty || !mounted) return;

    final kept = <XFile>[];
    for (final file in picked) {
      if (await file.length() > _maxImageBytes) {
        if (mounted) {
          notifyError(
            context,
            l10n.tp('channel.imageTooLarge', {'name': file.name}),
          );
        }
        continue;
      }
      kept.add(file);
    }
    if (!mounted || kept.isEmpty) return;

    setState(() {
      final room = _maxImages - _images.length;
      if (kept.length > room) {
        notifyError(
          context,
          l10n.tp('channel.tooManyImages', {'max': '$_maxImages'}),
        );
        _images.addAll(kept.take(room));
      } else {
        _images.addAll(kept);
      }
    });
  }

  void _removeImage(int index) => setState(() => _images.removeAt(index));

  Future<void> _send() async {
    if (!_canSend) return;
    FocusScope.of(context).unfocus();
    setState(() => _sending = true);
    final l10n = context.l10n;
    try {
      await ref
          .read(repositoryProvider)
          .notifyChannel(
            widget.channelId,
            _title.text.trim(),
            _body.text.trim(),
            imagePaths: _images.map((f) => f.path).toList(),
          );
      if (!mounted) return;
      _title.clear();
      _body.clear();
      setState(_images.clear);
      notifySuccess(context, l10n.t('channel.messageSent'));
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('channel.sendFailed'), e);
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        TextField(
          controller: _title,
          textInputAction: TextInputAction.next,
          onChanged: (_) => setState(() {}),
          decoration: InputDecoration(
            labelText: l10n.t('channel.sendTitle'),
            hintText: l10n.t('channel.titlePlaceholder'),
          ),
        ),
        const SizedBox(height: 12),
        TextField(
          controller: _body,
          minLines: 3,
          maxLines: 6,
          onChanged: (_) => setState(() {}),
          decoration: InputDecoration(
            labelText: l10n.t('channel.sendBody'),
            hintText: l10n.t('channel.bodyPlaceholder'),
            alignLabelWithHint: true,
          ),
        ),
        const SizedBox(height: 16),
        Row(
          children: [
            Text(
              l10n.t('channel.images'),
              style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 14),
            ),
            const Spacer(),
            TextButton.icon(
              onPressed: _images.length >= _maxImages ? null : _pickImages,
              icon: const Icon(Icons.add_photo_alternate_outlined, size: 18),
              label: Text(l10n.t('channel.addImages')),
            ),
          ],
        ),
        if (_images.isEmpty)
          Text(
            l10n.tp('channel.imagesHint', {'max': '$_maxImages'}),
            style: TextStyle(
              fontSize: 12,
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          )
        else
          _ImagePreviewStrip(images: _images, onRemove: _removeImage),
        const SizedBox(height: 16),
        FilledButton.icon(
          onPressed: (_sending || !_canSend) ? null : _send,
          icon: _sending
              ? const SizedBox(
                  height: 18,
                  width: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Icon(Icons.send_rounded, size: 18),
          label: Text(l10n.t('channel.send')),
        ),
      ],
    );
  }
}

/// A horizontal row of selected-image thumbnails, each with a remove button.
class _ImagePreviewStrip extends StatelessWidget {
  const _ImagePreviewStrip({required this.images, required this.onRemove});

  final List<XFile> images;
  final void Function(int index) onRemove;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 88,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        itemCount: images.length,
        separatorBuilder: (_, _) => const SizedBox(width: 8),
        itemBuilder: (context, i) => Stack(
          children: [
            ClipRRect(
              borderRadius: BorderRadius.circular(8),
              child: Image.file(
                File(images[i].path),
                width: 88,
                height: 88,
                fit: BoxFit.cover,
              ),
            ),
            Positioned(
              top: 2,
              right: 2,
              child: GestureDetector(
                onTap: () => onRemove(i),
                child: Container(
                  decoration: const BoxDecoration(
                    color: Colors.black54,
                    shape: BoxShape.circle,
                  ),
                  padding: const EdgeInsets.all(2),
                  child: const Icon(Icons.close, size: 16, color: Colors.white),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
