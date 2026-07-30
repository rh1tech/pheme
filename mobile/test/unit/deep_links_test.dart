import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/core/deep_links.dart';

/// The parser is the whole trust boundary for links arriving from outside the app, so it is
/// tested as one: what it accepts, what it refuses, and — most importantly — what it refuses to
/// believe about a server address.
void main() {
  group('invite links', () {
    test('carries the code and the server', () {
      final link = parseDeepLink(
        Uri.parse('pheme://invite?code=Ab1_cD&server=https://host.example/x7f'),
      );
      expect(link, isA<InviteLink>());
      final invite = link! as InviteLink;
      expect(invite.code, 'Ab1_cD');
      expect(invite.server, 'https://host.example/x7f');
    });

    test('a server-less invitation is still an invitation', () {
      final link = parseDeepLink(Uri.parse('pheme://invite?code=Ab1_cD'));
      expect((link! as InviteLink).server, isNull);
    });

    test('a bare host is given https, and a trailing slash is dropped', () {
      final link = parseDeepLink(
        Uri.parse('pheme://invite?code=x&server=host.example/x7f/'),
      );
      expect((link! as InviteLink).server, 'https://host.example/x7f');
    });

    test('a code is not case-folded — the alphabet is case-sensitive', () {
      final link = parseDeepLink(Uri.parse('pheme://invite?code=AbC'));
      expect((link! as InviteLink).code, 'AbC');
    });

    // A link naming an unusable server must not take the invitation down with it: the code is
    // still good, and the app keeps pointing wherever it already did.
    test('an unusable server address is dropped, not passed through', () {
      for (final bad in ['javascript:alert(1)', 'ftp://host.example', '   ']) {
        final link = parseDeepLink(
          Uri.parse('pheme://invite?code=x&server=${Uri.encodeComponent(bad)}'),
        );
        expect(link, isA<InviteLink>(), reason: bad);
        expect((link! as InviteLink).server, isNull, reason: bad);
      }
    });

    test('an invitation with no code is not a link', () {
      expect(parseDeepLink(Uri.parse('pheme://invite')), isNull);
      expect(parseDeepLink(Uri.parse('pheme://invite?code=')), isNull);
      expect(parseDeepLink(Uri.parse('pheme://invite?code=%20%20')), isNull);
    });
  });

  group('join links', () {
    test('carries the reference verbatim', () {
      for (final ref in [
        'ch_ab12cd34',
        'my-alias',
        'ch_ab12cd34@host.example',
      ]) {
        final link = parseDeepLink(
          Uri.parse('pheme://join?ref=${Uri.encodeComponent(ref)}'),
        );
        expect((link! as JoinLink).ref, ref);
      }
    });

    test('a join with no reference is not a link', () {
      expect(parseDeepLink(Uri.parse('pheme://join')), isNull);
    });
  });

  group('server links', () {
    test('normalises the address', () {
      final link = parseDeepLink(
        Uri.parse('pheme://server?url=host.example/x7f'),
      );
      expect((link! as ServerLink).server, 'https://host.example/x7f');
    });

    // Unlike an invitation, a server link IS its address — an unusable one leaves nothing.
    test('an unusable address is not a link at all', () {
      expect(
        parseDeepLink(Uri.parse('pheme://server?url=ftp%3A%2F%2Fhost.example')),
        isNull,
      );
      expect(parseDeepLink(Uri.parse('pheme://server?url=')), isNull);
      expect(parseDeepLink(Uri.parse('pheme://server')), isNull);
    });
  });

  group('what is not ours', () {
    test('another scheme is refused, however familiar it looks', () {
      expect(
        parseDeepLink(Uri.parse('https://host.example/login?invite=x')),
        isNull,
      );
      expect(parseDeepLink(Uri.parse('phemex://invite?code=x')), isNull);
    });

    test('an unknown action is refused', () {
      expect(parseDeepLink(Uri.parse('pheme://transfer?to=someone')), isNull);
      expect(parseDeepLink(Uri.parse('pheme://')), isNull);
    });

    test('the action is matched case-insensitively', () {
      expect(
        parseDeepLink(Uri.parse('pheme://INVITE?code=x')),
        isA<InviteLink>(),
      );
    });

    // Written by hand, or by something that insists on an authority component.
    test('the path form works as well as the host form', () {
      expect(
        parseDeepLink(Uri.parse('pheme:///invite?code=x')),
        isA<InviteLink>(),
      );
    });
  });
}
