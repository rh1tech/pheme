import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import type { ReactNode } from 'react'
import { AuthProvider } from './auth/AuthProvider'
import { Layout } from './components/Layout'
import { RequireAuth } from './components/RequireAuth'
import { RequireAdmin } from './components/RequireAdmin'
import { ScrollToTop } from './components/ScrollToTop'
import { LoginPage } from './pages/LoginPage'
import { ForgotPasswordPage } from './pages/ForgotPasswordPage'
import { DashboardPage } from './pages/DashboardPage'
import { ChannelPage } from './pages/ChannelPage'
import { JoinPage } from './pages/JoinPage'
import { MessagePage } from './pages/MessagePage'
import { AdminOverviewPage } from './pages/admin/AdminOverviewPage'
import { AdminUsersPage } from './pages/admin/AdminUsersPage'
import { AdminChannelsPage } from './pages/admin/AdminChannelsPage'
import { AdminChannelPage } from './pages/AdminChannelPage'

function adminRoute(element: ReactNode) {
  return <RequireAdmin>{element}</RequireAdmin>
}

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <ScrollToTop />
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/forgot-password" element={<ForgotPasswordPage />} />
          <Route
            element={
              <RequireAuth>
                <Layout />
              </RequireAuth>
            }
          >
            <Route path="/" element={<DashboardPage />} />
            <Route path="/join" element={<JoinPage />} />
            <Route path="/channels/:id" element={<ChannelPage />} />
            <Route path="/channels/:id/messages/:messageId" element={<MessagePage />} />
            <Route path="/admin" element={adminRoute(<AdminOverviewPage />)} />
            <Route path="/admin/users" element={adminRoute(<AdminUsersPage />)} />
            <Route path="/admin/channels" element={adminRoute(<AdminChannelsPage />)} />
            <Route path="/admin/channels/:id" element={adminRoute(<AdminChannelPage />)} />
          </Route>
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  )
}
