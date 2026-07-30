import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';

import '../l10n/app_localizations.dart';

/// Whether this platform can hand us a freshly taken photo.
///
/// image_picker only implements [ImageSource.camera] on Android and iOS. On desktop the call throws,
/// so the choice is not offered there and the picker goes straight to the file browser.
bool get canCapturePhoto => !kIsWeb && (Platform.isAndroid || Platform.isIOS);

/// Asks where a photo should come from: the camera, or the library.
///
/// Returns null if the sheet was dismissed. On a platform without a camera the sheet is skipped
/// entirely and [ImageSource.gallery] is returned, so callers can treat this as "pick a source"
/// rather than special-casing the platform themselves.
Future<ImageSource?> askPhotoSource(BuildContext context) async {
  if (!canCapturePhoto) return ImageSource.gallery;

  final l10n = AppLocalizations.of(context);
  return showModalBottomSheet<ImageSource>(
    context: context,
    showDragHandle: true,
    builder: (sheet) => SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          ListTile(
            leading: const Icon(Icons.photo_camera_outlined),
            title: Text(l10n.t('chat.takePhoto')),
            onTap: () => Navigator.of(sheet).pop(ImageSource.camera),
          ),
          ListTile(
            leading: const Icon(Icons.photo_library_outlined),
            title: Text(l10n.t('chat.chooseFromLibrary')),
            onTap: () => Navigator.of(sheet).pop(ImageSource.gallery),
          ),
        ],
      ),
    ),
  );
}
