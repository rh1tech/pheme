import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import type { ReactNode } from 'react'
import { AuthProvider } from './auth/AuthProvider'
import { Layout } from './components/Layout'
import { RequireAuth } from './components/RequireAuth'
import { RequireAdmin } from './components/RequireAdmin'
import { LoginPage } from './pages/LoginPage'
import { DashboardPage } from './pages/DashboardPage'
import { ChannelPage } from './pages/ChannelPage'
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
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route
            element={
              <RequireAuth>
                <Layout />
              </RequireAuth>
            }
          >
            <Route path="/" element={<DashboardPage />} />
            <Route path="/channels/:id" element={<ChannelPage />} />
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
