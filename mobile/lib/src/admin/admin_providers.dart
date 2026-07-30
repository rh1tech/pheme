import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';
import 'admin_repository.dart';

/// The admin API, on the same Dio every other request goes through.
///
/// A plain Provider rather than anything stateful: the admin screens hold their own page state, and
/// there is nothing here worth caching between them — a moderation list that showed what it showed
/// last time an admin looked is worse than one that fetches.
final adminRepositoryProvider = Provider<AdminRepository>(
  (ref) => AdminRepository(ref.watch(dioProvider)),
);
