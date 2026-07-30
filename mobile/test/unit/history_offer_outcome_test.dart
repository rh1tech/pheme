// What must happen after a history offer goes wrong.
//
// Fetching an offer CONSUMES it: the server deletes the blob as it hands it over, so an offer that
// arrives and is then dropped is gone from the wire for good. The transcript still exists on the
// device that offered it, which means a failure is recoverable — but only by asking again, and only
// if the caller can tell "this failed in a way another attempt could survive" from "this was
// refused on its merits".
//
// Before this distinction existed every failure returned the same `false` and the caller did
// nothing with it, so a transcript that was fetched and then could not be stored simply never
// arrived, and nothing anywhere said so.

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/crypto/mls_service.dart';

void main() {
  group('an offer outcome says whether to ask again', () {
    // The cases where the blob was consumed and the history is still missing. These MUST lead to a
    // fresh request or the transfer never happens.
    test('lost is the only outcome that means "consumed and still missing"', () {
      expect(HistoryOfferResult.values, contains(HistoryOfferResult.lost));
      // Named so the caller cannot confuse it with a refusal: the two look identical from the
      // outside — no history — and want opposite responses.
      expect(HistoryOfferResult.lost, isNot(HistoryOfferResult.refused));
      expect(HistoryOfferResult.lost, isNot(HistoryOfferResult.ignored));
    });

    // A refusal must NOT be retried. Retrying a bad signature means fetching the same forgery
    // again, and a device that loops on it is a device an insider can keep busy.
    test(
      'refused and ignored are distinct from each other and from accepted',
      () {
        expect(
          {
            HistoryOfferResult.accepted,
            HistoryOfferResult.ignored,
            HistoryOfferResult.refused,
            HistoryOfferResult.lost,
          }.length,
          4,
          reason: 'four outcomes, four different responses',
        );
      },
    );

    test('every outcome is handled — a new one cannot be added silently', () {
      // An exhaustive switch over the enum. If a case is added later, this fails to compile rather
      // than defaulting to "do nothing", which for this type means "lose the history quietly".
      for (final result in HistoryOfferResult.values) {
        final asksAgain = switch (result) {
          HistoryOfferResult.lost => true,
          HistoryOfferResult.accepted => false,
          HistoryOfferResult.ignored => false,
          HistoryOfferResult.refused => false,
        };
        expect(
          asksAgain,
          result == HistoryOfferResult.lost,
          reason: 'only a consumed-and-lost offer justifies asking again',
        );
      }
    });
  });
}
