import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';
import '../models/models.dart';

Widget _networkImage(
  BuildContext context,
  WidgetRef ref,
  MessageImage image,
  BoxFit fit,
) {
  final scheme = Theme.of(context).colorScheme;
  return CachedNetworkImage(
    imageUrl: ref.read(repositoryProvider).imageUrl(image.id),
    fit: fit,
    placeholder: (context, _) =>
        ColoredBox(color: scheme.surfaceContainerHighest),
    errorWidget: (context, _, _) => ColoredBox(
      color: scheme.surfaceContainerHighest,
      child: Icon(Icons.broken_image_outlined, color: scheme.outline),
    ),
  );
}

/// A single cover image (the first of a message's images) for use in lists, with
/// a count badge when the message has more than one image.
class MessageCover extends ConsumerWidget {
  const MessageCover({super.key, required this.images});

  final List<MessageImage> images;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (images.isEmpty) return const SizedBox.shrink();
    final ratio = images.first.aspectRatio.clamp(0.75, 1.91);
    return Stack(
      children: [
        AspectRatio(
          aspectRatio: ratio.toDouble(),
          child: _networkImage(context, ref, images.first, BoxFit.cover),
        ),
        if (images.length > 1)
          Positioned(
            top: 8,
            right: 8,
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
              decoration: BoxDecoration(
                color: Colors.black54,
                borderRadius: BorderRadius.circular(999),
              ),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  const Icon(
                    Icons.photo_library_outlined,
                    size: 13,
                    color: Colors.white,
                  ),
                  const SizedBox(width: 4),
                  Text(
                    '${images.length}',
                    style: const TextStyle(color: Colors.white, fontSize: 12),
                  ),
                ],
              ),
            ),
          ),
      ],
    );
  }
}

/// An Instagram-style image carousel: a swipeable PageView of cached network
/// images with page-dot indicators. A single image renders without dots.
class MessageCarousel extends ConsumerStatefulWidget {
  const MessageCarousel({super.key, required this.images});

  final List<MessageImage> images;

  @override
  ConsumerState<MessageCarousel> createState() => _MessageCarouselState();
}

class _MessageCarouselState extends ConsumerState<MessageCarousel> {
  final _controller = PageController();
  int _current = 0;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final ratio = widget.images.first.aspectRatio.clamp(0.75, 1.91);

    return Column(
      children: [
        AspectRatio(
          aspectRatio: ratio.toDouble(),
          child: PageView.builder(
            controller: _controller,
            itemCount: widget.images.length,
            onPageChanged: (i) => setState(() => _current = i),
            itemBuilder: (context, i) =>
                _networkImage(context, ref, widget.images[i], BoxFit.cover),
          ),
        ),
        if (widget.images.length > 1)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 8),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                for (var i = 0; i < widget.images.length; i++)
                  Container(
                    width: 6,
                    height: 6,
                    margin: const EdgeInsets.symmetric(horizontal: 3),
                    decoration: BoxDecoration(
                      shape: BoxShape.circle,
                      color: i == _current
                          ? scheme.primary
                          : scheme.outlineVariant,
                    ),
                  ),
              ],
            ),
          ),
      ],
    );
  }
}
