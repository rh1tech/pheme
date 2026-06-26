import { Component, type ReactNode } from 'react'
import { Button, Center, Stack, Text, Title } from '@mantine/core'
import { useTranslation } from 'react-i18next'

interface ErrorBoundaryProps {
  children: ReactNode
}

interface ErrorBoundaryState {
  hasError: boolean
}

/** Translated fallback shown when a render error is caught. Kept as a function
 *  component so it can use hooks (the boundary itself must be a class). */
function ErrorFallback() {
  const { t } = useTranslation()
  return (
    <Center mih="100dvh" p="md">
      <Stack align="center" gap="sm" maw={420} ta="center">
        <Title order={3}>{t('common.errorTitle')}</Title>
        <Text c="dimmed" size="sm">
          {t('common.errorBody')}
        </Text>
        <Button onClick={() => window.location.reload()} mt="xs">
          {t('common.reload')}
        </Button>
      </Stack>
    </Center>
  )
}

/**
 * Catches render-time exceptions anywhere below it and shows a recoverable
 * fallback instead of unmounting the whole React tree (which leaves a blank
 * screen). Without this, a single render throw blanks the entire interface.
 */
export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { hasError: false }

  static getDerivedStateFromError(): ErrorBoundaryState {
    return { hasError: true }
  }

  componentDidCatch(error: unknown) {
    // Surface the error for diagnostics; the fallback handles the UI.
    console.error('Unhandled render error:', error)
  }

  render() {
    if (this.state.hasError) return <ErrorFallback />
    return this.props.children
  }
}
