import { Modal, type ModalProps } from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'

/**
 * A Modal that presents as a centered dialog on desktop and a bottom sheet on
 * mobile (slides up from the bottom edge, full width, square bottom corners).
 * Keeps every other Modal prop intact so callers use it like a drop-in Modal.
 */
export function ResponsiveModal(props: ModalProps) {
  const isMobile = useMediaQuery('(max-width: 48em)')

  if (!isMobile) return <Modal {...props} />

  return (
    <Modal
      {...props}
      centered={false}
      transitionProps={{ transition: 'slide-up', duration: 220 }}
      styles={{
        inner: { alignItems: 'flex-end', padding: 0 },
        content: {
          width: '100%',
          maxWidth: '100%',
          maxHeight: '92vh',
          borderBottomLeftRadius: 0,
          borderBottomRightRadius: 0,
        },
      }}
    />
  )
}
