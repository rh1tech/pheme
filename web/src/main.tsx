import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { NavigationProgress } from '@mantine/nprogress'
import '@mantine/core/styles.css'
import '@mantine/notifications/styles.css'
import '@mantine/nprogress/styles.css'
import './styles.css'
import './i18n'
import { theme } from './theme'
import App from './App.tsx'
import { ErrorBoundary } from './components/ErrorBoundary'
import { lockViewportZoom } from './lib/mobile'

lockViewportZoom()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MantineProvider theme={theme} defaultColorScheme="auto">
      <NavigationProgress />
      <Notifications />
      <ErrorBoundary>
        <App />
      </ErrorBoundary>
    </MantineProvider>
  </StrictMode>,
)
