import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import type { ReactNode } from 'react'
import { AuthProvider } from './auth/AuthProvider'
import { Layout } from './components/Layout'
import { ChatShell } from './components/chat/ChatShell'
import { DiscussionPane } from './components/chat/DiscussionPane'
import { RequireAuth } from './components/RequireAuth'
import { RequireAdmin } from './components/RequireAdmin'
import { ScrollToTop } from './components/ScrollToTop'
import { UpdatePrompt } from './components/UpdatePrompt'
import { LoginPage } from './pages/LoginPage'
import { ForgotPasswordPage } from './pages/ForgotPasswordPage'
import { ChatEmptyState } from './pages/chat/ChatEmptyState'
import { ConversationRoute } from './pages/chat/ConversationRoute'
import { ConversationChatRoute } from './pages/chat/ConversationChatRoute'
import { JoinPage } from './pages/JoinPage'
import { ProfilePage } from './pages/ProfilePage'
import { AdminOverviewPage } from './pages/admin/AdminOverviewPage'
import { AdminUsersPage } from './pages/admin/AdminUsersPage'
import { AdminChannelsPage } from './pages/admin/AdminChannelsPage'
import { AdminCommentsPage } from './pages/admin/AdminCommentsPage'
import { AdminChannelPage } from './pages/AdminChannelPage'

function adminRoute(element: ReactNode) {
  return <RequireAdmin>{element}</RequireAdmin>
}

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <ScrollToTop />
        {/* Above every route: a tab is just as stale on the login page as in a chat. */}
        <UpdatePrompt />
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/forgot-password" element={<ForgotPasswordPage />} />

          {/* The chat surface: a fixed-height, three-pane app. It owns the
              channel list and every channel route. */}
          <Route
            element={
              <RequireAuth>
                <ChatShell />
              </RequireAuth>
            }
          >
            <Route path="/" element={<ChatEmptyState />} />
            <Route path="/channels/:id" element={<ConversationRoute />}>
              {/* Nested, so opening a discussion does not remount the feed. */}
              <Route path="messages/:messageId" element={<DiscussionPane />} />
            </Route>
            {/* Private conversations sit beside channels in the same shell. */}
            <Route path="/chats/:id" element={<ConversationChatRoute />} />
          </Route>

          {/* Everything else keeps the scrolling container layout: these are
              documents and tables, not conversations. */}
          <Route
            element={
              <RequireAuth>
                <Layout />
              </RequireAuth>
            }
          >
            <Route path="/join" element={<JoinPage />} />
            <Route path="/profile" element={<ProfilePage />} />
            <Route path="/admin" element={adminRoute(<AdminOverviewPage />)} />
            <Route path="/admin/users" element={adminRoute(<AdminUsersPage />)} />
            <Route path="/admin/channels" element={adminRoute(<AdminChannelsPage />)} />
            <Route path="/admin/comments" element={adminRoute(<AdminCommentsPage />)} />
            <Route path="/admin/channels/:id" element={adminRoute(<AdminChannelPage />)} />
          </Route>

          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  )
}
