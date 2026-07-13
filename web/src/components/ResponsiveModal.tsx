import { Modal, type ModalProps } from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import { useVisualViewportRect } from '../hooks/useVisualViewport'

/**
 * A Modal that presents as a centered dialog on desktop and a bottom sheet on
 * mobile (slides up from the bottom edge, full width, square bottom corners).
 * Keeps every other Modal prop intact so callers use it like a drop-in Modal.
 *
 * The sheet is positioned against the VISIBLE viewport, not the page. Mantine's
 * overlay is `position: fixed`, which iOS lays out against the layout viewport — and
 * iOS does not shrink that when the software keyboard opens; it shrinks the visual
 * viewport. So a sheet pinned to the bottom of the page ends up behind the keyboard,
 * taking the field the user was trying to type into with it. Anchoring to the visual
 * viewport keeps it above the keyboard.
 */
export function ResponsiveModal(props: ModalProps) {
  const isMobile = useMediaQuery('(max-width: 48em)')
  const viewport = useVisualViewportRect()

  if (!isMobile) return <Modal {...props} />

  // Without visualViewport (older engines) fall back to the page box — the previous
  // behaviour, which is correct anywhere the keyboard does not overlay content.
  const inner = viewport
    ? {
        alignItems: 'flex-end' as const,
        padding: 0,
        top: viewport.offsetTop,
        height: viewport.height,
        bottom: 'auto',
      }
    : { alignItems: 'flex-end' as const, padding: 0 }

  return (
    <Modal
      {...props}
      centered={false}
      transitionProps={{ transition: 'slide-up', duration: 220 }}
      styles={{
        inner,
        content: {
          width: '100%',
          maxWidth: '100%',
          // Bounded by what is actually visible, so a tall dialog scrolls inside the
          // sheet instead of growing down underneath the keyboard.
          maxHeight: viewport ? `${Math.round(viewport.height * 0.92)}px` : '92vh',
          borderBottomLeftRadius: 0,
          borderBottomRightRadius: 0,
        },
        body: { overflowY: 'auto' },
      }}
    />
  )
}
