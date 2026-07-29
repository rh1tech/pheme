# Licensing and attribution — Pheme web client

The Pheme **web client** (this `web/` directory) is licensed under the
**GNU General Public License v3** — see [LICENSE](./LICENSE) and the repository
licensing summary in [`../LICENSING.md`](../LICENSING.md).

It is licensed this way because parts of its chat behaviour are derived from
**Telegram Web K**, which is GPL v3:

- Source: <https://github.com/morethanwords/tweb> (Telegram Web K)
- Copyright © Eduard Kuzmenko and Telegram Web K contributors
- Licence: GNU General Public License v3

The GPL's obligations attach on **distribution**, and this client is distributed
to every browser that loads the app — so the licence and this notice live here
from the moment the derived code does, not at some later date.

## What is derived

The following behaviours are ports of Telegram Web K logic, adapted to this
codebase (React + Mantine, rather than Solid.js). They are not verbatim copies —
the surrounding architecture differs — but they are derivative works and are
treated as such:

| Here | Derived from |
|------|--------------|
| `src/hooks/useChatScroll.ts` — distance-from-bottom anchoring across a prepend, and the "scrolled to end" tolerance | `src/helpers/scrollSaver.ts` (`ScrollSaver`) |
| `src/hooks/useChatScroll.ts` / `src/pages/chat/ConversationRoute.tsx` — opening a channel at the first unread message rather than at the newest, including the "exactly one unread → go to the bottom instead" rule | `src/components/chat/bubbles.ts` (`performHistoryResult`, around the `readMaxId` / `followingUnread` branch) |
| `src/components/chat/UnreadDivider.tsx` — the unread separator, shown above the first unread message and only when it is not the newest one | `src/components/chat/bubbles.ts` (`is-first-unread` / `attachedUnreadBubble`) |
| `src/components/chat/MediaViewer.tsx` — the fullscreen photo viewer's interaction model: Escape to close, ArrowLeft/ArrowRight through the set, swipe to navigate, drag-down to dismiss, zoom | `src/components/appMediaViewerBase.ts` |

## What is not derived

Everything else in `web/` — the Pheme-specific UI, the API client, the channel /
message / comment model, the push-notification plumbing — is original work.

The Pheme **API and services** (`api/`) share no code with Telegram. They are
available under the repository's GPLv3 community license and may separately be
eligible for commercial licensing by their copyright holder. The
Telegram-derived web code is not eligible for that commercial license without
permission from its copyright holders.
