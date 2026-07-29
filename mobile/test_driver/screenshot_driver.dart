// Driver for the screenshot pass.
//
// integration_test can capture the framebuffer, but only the driver side can
// write it anywhere, so this is the half that puts bytes on disk. SHOT_DIR names
// the platform folder under ../screenshots.
import 'dart:io';

import 'package:integration_test/integration_test_driver_extended.dart';

Future<void> main() async {
  final dir = Platform.environment['SHOT_DIR'] ?? 'mobile';
  await integrationDriver(
    onScreenshot: (String name, List<int> bytes, [Map<String, Object?>? args]) async {
      final file = File('../screenshots/$dir/$name.png');
      await file.create(recursive: true);
      await file.writeAsBytes(bytes);
      stdout.writeln('wrote ${file.path}');
      return true;
    },
  );
}
