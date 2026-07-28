// Who a direct chat is named after.
//
// This is one line of logic and it was wrong in a way nobody could see: with an empty "my id" the
// first member won, and in a chat you started that is you — so opening a new chat with somebody
// showed your own name and your own avatar in the header. It righted itself on the next launch,
// because by then the id had been read after sign-in rather than before, which is exactly why it
// survived so long.

import 'package:flutter/widgets.dart' show Locale;
import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/chat/conversation_title.dart';
import 'package:pheme_mobile/src/l10n/app_localizations.dart';
import 'package:pheme_mobile/src/models/chat_models.dart';
import 'package:pheme_mobile/src/models/models.dart';

ConversationMember _member(String id, String name) => ConversationMember(
  id: 'm_$id',
  conversationId: 'c1',
  userId: id,
  role: ChannelRole.user,
  joinedAt: '',
  user: PublicUser(id: id, displayName: name),
);

Conversation _direct() => Conversation(
  id: 'c1',
  kind: ConversationKind.direct,
  createdBy: 'me',
  createdAt: '',
  members: [_member('me', 'Mikhail Matveev'), _member('them', 'Juliett')],
);

void main() {
  final l10n = AppLocalizations(const Locale('en'));

  group('Conversation.otherMember', () {
    test('is the member who is not me', () {
      expect(_direct().otherMember('me')?.userId, 'them');
    });

    test('is the member who is not me, whichever order they arrive in', () {
      final flipped = Conversation(
        id: 'c1',
        kind: ConversationKind.direct,
        createdBy: 'me',
        createdAt: '',
        members: [_member('them', 'Juliett'), _member('me', 'Mikhail Matveev')],
      );
      expect(flipped.otherMember('me')?.userId, 'them');
    });

    test('is NOBODY when my id is unknown, rather than whoever comes first', () {
      // The regression. Returning members.first here is what put the signed-in user's own name and
      // face at the top of a chat with someone else.
      expect(_direct().otherMember(''), isNull);
    });
  });

  group('conversationTitle', () {
    test('names a direct chat after the other person', () {
      expect(conversationTitle(_direct(), 'me', l10n), 'Juliett');
    });

    test('never names a direct chat after ME when my id is unknown', () {
      final title = conversationTitle(_direct(), '', l10n);
      expect(title, isNot('Mikhail Matveev'));
      expect(title, l10n.t('chat.newChat'));
    });
  });
}
