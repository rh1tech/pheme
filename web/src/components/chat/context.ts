import type { ChannelListApi } from '../../hooks/useChannelList'
import type { ConversationListApi } from '../../hooks/useConversationList'

/** What the chat shell passes down to whatever is rendered in the conversation slot. */
export interface ChatOutletContext {
  list: ChannelListApi
  conversations: ConversationListApi
  /**
   * Bumped every time a channel is picked in the sidebar — including a re-click of
   * the channel already open. The composer focuses on each change, which the
   * channel id alone cannot express: re-selecting the open channel leaves the id
   * untouched, and the reader still expects the cursor in the message box.
   */
  composerFocus: number
}
