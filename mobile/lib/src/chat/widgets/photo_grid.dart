// Photos inside a message bubble.
//
// Each one is fetched from the server as ciphertext and opened with the key that came inside the
// message. So a photo cannot be shown by pointing an Image widget at a URL — there is nothing at that
// URL but sealed bytes, and the key never leaves the app.
//
// The space is reserved from the FIRST FRAME, using the dimensions carried in the message. A bubble
// that does not know how tall a photo will be has to guess, and when the bytes land and the guess was
// wrong the whole feed jumps under the reader's thumb. Knowing the shape in advance is the difference
// between a photo appearing and a photo shoving.

import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../crypto/chat_content.dart';
import '../chat_providers.dart';

/// One decrypted photo, by conversation and blob id.
///
/// Unlike a message, a photo can safely be fetched more than once — the blob stays on the server and
/// the key stays in the message, so nothing is consumed by reading it. That makes this a cache for
/// speed rather than for correctness, which is the opposite of everything else in this app.
final photoProvider =
    FutureProvider.family<
      Uint8List,
      ({String conversationId, ChatPhoto photo})
    >((ref, args) async {
      return ref
          .read(mlsServiceProvider)
          .fetchPhoto(args.conversationId, args.photo);
    });

class PhotoGrid extends StatelessWidget {
  const PhotoGrid({
    super.key,
    required this.conversationId,
    required this.photos,
  });

  final String conversationId;
  final List<ChatPhoto> photos;

  @override
  Widget build(BuildContext context) {
    if (photos.isEmpty) return const SizedBox.shrink();

    // One photo keeps its own shape. Several are squared off into a grid, because a row of wildly
    // different aspect ratios reads as clutter rather than as a set.
    if (photos.length == 1) {
      return _Photo(conversationId: conversationId, photo: photos.first);
    }

    return GridView.count(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      crossAxisCount: 2,
      mainAxisSpacing: 2,
      crossAxisSpacing: 2,
      children: [
        for (final photo in photos)
          _Photo(
            conversationId: conversationId,
            photo: photo,
            fit: BoxFit.cover,
            square: true,
          ),
      ],
    );
  }
}

class _Photo extends ConsumerWidget {
  const _Photo({
    required this.conversationId,
    required this.photo,
    this.fit = BoxFit.contain,
    this.square = false,
  });

  final String conversationId;
  final ChatPhoto photo;
  final BoxFit fit;
  final bool square;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final bytes = ref.watch(
      photoProvider((conversationId: conversationId, photo: photo)),
    );

    final child = bytes.when(
      // The reserved space, not a spinner in an empty box: the box IS the photo's shape, so nothing
      // moves when the bytes arrive.
      loading: () => ColoredBox(
        color: theme.colorScheme.surfaceContainerHighest,
        child: const Center(
          child: SizedBox(
            width: 20,
            height: 20,
            child: CircularProgressIndicator(strokeWidth: 2),
          ),
        ),
      ),
      // A photo that will not open is not the photo that was sent, and showing something else would be
      // worse than saying so. It is also permanent — the key is in a message this device cannot read —
      // so this must not look like a retry is coming.
      error: (_, _) => ColoredBox(
        color: theme.colorScheme.surfaceContainerHighest,
        child: Center(
          child: Icon(
            Icons.broken_image_outlined,
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
      ),
      data: (data) => GestureDetector(
        onTap: () => _openFullScreen(context, data),
        child: Image.memory(data, fit: fit, gaplessPlayback: true),
      ),
    );

    return ClipRRect(
      borderRadius: BorderRadius.circular(12),
      child: square
          ? AspectRatio(aspectRatio: 1, child: child)
          : AspectRatio(aspectRatio: photo.aspectRatio, child: child),
    );
  }

  void _openFullScreen(BuildContext context, Uint8List data) {
    Navigator.of(context).push(
      MaterialPageRoute<void>(
        fullscreenDialog: true,
        builder: (_) => _FullScreenPhoto(bytes: data),
      ),
    );
  }
}

/// A photo, full bleed, pinchable. Black, because a photo viewer that is not black is a photo viewer
/// competing with the photo.
class _FullScreenPhoto extends StatelessWidget {
  const _FullScreenPhoto({required this.bytes});

  final Uint8List bytes;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        foregroundColor: Colors.white,
        elevation: 0,
      ),
      body: Center(
        child: InteractiveViewer(
          maxScale: 5,
          child: Image.memory(bytes, fit: BoxFit.contain),
        ),
      ),
    );
  }
}
