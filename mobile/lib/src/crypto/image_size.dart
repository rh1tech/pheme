// The pixel dimensions of an encoded image.
//
// They go inside the encrypted message, not because anyone needs them to decode the photo — a JPEG
// carries its own — but because the RECIPIENT needs them BEFORE it has the photo. A bubble that does
// not know how tall a photo will be has to guess, and when the bytes finally land and it guessed
// wrong, the whole feed jumps under the reader's thumb. Knowing the shape in advance means the space
// is reserved from the first frame.

import 'dart:typed_data';
import 'dart:ui' as ui;

class ImageSize {
  const ImageSize(this.width, this.height);

  final int width;
  final int height;
}

/// Decodes just far enough to learn the dimensions, then throws the image away.
///
/// Returns a 1x1 for anything that will not decode. The photo is about to be sealed and uploaded
/// regardless — a wrong aspect ratio is a cosmetic problem, and refusing to send somebody's picture
/// over one is not.
Future<ImageSize> decodeImageSize(Uint8List bytes) async {
  try {
    final codec = await ui.instantiateImageCodec(bytes);
    final frame = await codec.getNextFrame();
    final size = ImageSize(frame.image.width, frame.image.height);

    frame.image.dispose();
    codec.dispose();
    return size;
  } on Object {
    return const ImageSize(1, 1);
  }
}
