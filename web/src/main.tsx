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
import { registerServiceWorker } from './lib/sw'

lockViewportZoom()
registerServiceWorker()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MantineProvider theme={theme} defaultColorScheme="auto">
      <NavigationProgress />
      {/* Top-right, not the default bottom-right: on the chat surface the send
          button sits in the bottom-right corner, and a toast there covers it. */}
      <Notifications position="top-right" />
      <ErrorBoundary>
        <App />
      </ErrorBoundary>
    </MantineProvider>
  </StrictMode>,
)
