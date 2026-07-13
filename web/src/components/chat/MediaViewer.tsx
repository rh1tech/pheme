/*
 * Derived from Telegram Web K (GPL v3):
 *   https://github.com/morethanwords/tweb — src/components/appMediaViewerBase.ts
 * Specifically the interaction model: Escape to close, ArrowLeft/ArrowRight to
 * step through the set, swipe to navigate, drag-down to dismiss, and zoom.
 * See web/NOTICE.md.
 */
import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ActionIcon, Text } from '@mantine/core'
import { IconChevronLeft, IconChevronRight, IconDownload, IconX } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { imageUrl } from '../../lib/api'
import type { MessageImage } from '../../lib/types'

// A drag this far down releases the image and closes the viewer.
const DISMISS_PX = 110
// A horizontal flick this far steps to the neighbouring image.
const SWIPE_PX = 60
const ZOOM_SCALE = 2.5

export interface MediaViewerTarget {
  images: MessageImage[]
  index: number
}

interface MediaViewerProps {
  target: MediaViewerTarget
  onClose: () => void
}

interface Point {
  x: number
  y: number
}

export function MediaViewer({ target, onClose }: MediaViewerProps) {
  const { t } = useTranslation()
  const [index, setIndex] = useState(target.index)
  const [scale, setScale] = useState(1)
  const [offset, setOffset] = useState<Point>({ x: 0, y: 0 })
  // Live drag offset. While dragging an unzoomed image this doubles as the
  // dismiss gesture, so it also drives the backdrop's opacity.
  const [drag, setDrag] = useState<Point>({ x: 0, y: 0 })
  const dragStart = useRef<Point | null>(null)
  // Whether a drag is in flight is state, not a ref: it decides whether the image
  // animates, and anything the render reads has to be something the render can see.
  const [dragging, setDragging] = useState(false)
  const zoomed = scale > 1

  const images = target.images
  const image = images[index]
  const many = images.length > 1

  const reset = useCallback(() => {
    setScale(1)
    setOffset({ x: 0, y: 0 })
    setDrag({ x: 0, y: 0 })
  }, [])

  const step = useCallback(
    (delta: number) => {
      setIndex((i) => {
        const next = i + delta
        if (next < 0 || next >= images.length) return i
        return next
      })
      reset()
    },
    [images.length, reset],
  )

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
      else if (e.key === 'ArrowRight') step(1)
      else if (e.key === 'ArrowLeft') step(-1)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose, step])

  // The page behind must not scroll while the viewer owns the screen.
  useEffect(() => {
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = previous
    }
  }, [])

  function toggleZoom() {
    if (zoomed) reset()
    else setScale(ZOOM_SCALE)
  }

  function onPointerDown(e: React.PointerEvent) {
    // Only a primary drag; ignore secondary buttons.
    if (e.button !== 0) return
    dragStart.current = { x: e.clientX, y: e.clientY }
    setDragging(true)
    e.currentTarget.setPointerCapture(e.pointerId)
  }

  function onPointerMove(e: React.PointerEvent) {
    const start = dragStart.current
    if (!start) return
    setDrag({ x: e.clientX - start.x, y: e.clientY - start.y })
  }

  function onPointerUp() {
    const moved = drag
    dragStart.current = null
    setDragging(false)

    if (zoomed) {
      // Zoomed in, a drag pans the image and stays put.
      setOffset((o) => ({ x: o.x + moved.x, y: o.y + moved.y }))
      setDrag({ x: 0, y: 0 })
      return
    }

    // Dragging down far enough throws the image away — the gesture people expect
    // from a photo viewer, and the only dismissal a thumb can reach one-handed.
    if (moved.y > DISMISS_PX && Math.abs(moved.y) > Math.abs(moved.x)) {
      onClose()
      return
    }
    if (many && Math.abs(moved.x) > SWIPE_PX && Math.abs(moved.x) > Math.abs(moved.y)) {
      step(moved.x < 0 ? 1 : -1)
      return
    }
    setDrag({ x: 0, y: 0 })
  }

  if (!image) return null

  // Fade the backdrop out as the image is pulled away, so the gesture reads as
  // "dropping" the photo rather than sliding a panel.
  const dismissProgress = zoomed ? 0 : Math.min(1, Math.max(0, drag.y) / (DISMISS_PX * 2))
  const x = offset.x + drag.x
  const y = offset.y + drag.y

  const view = (
    <div
      className="pheme-viewer"
      data-testid="media-viewer"
      role="dialog"
      aria-modal="true"
      aria-label={t('channel.imageAlt', { index: index + 1, total: images.length })}
      style={{ backgroundColor: `rgba(0, 0, 0, ${0.92 - dismissProgress * 0.7})` }}
      onClick={(e) => {
        // Clicking the backdrop (not the image) closes, as in Telegram.
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <header className="pheme-viewer-bar">
        {many && (
          <Text size="sm" c="#fff">
            {index + 1} / {images.length}
          </Text>
        )}
        <div style={{ flex: 1 }} />
        <ActionIcon
          component="a"
          href={imageUrl(image.id)}
          target="_blank"
          rel="noreferrer"
          download
          variant="subtle"
          color="gray"
          aria-label={t('channel.downloadImage')}
        >
          <IconDownload size={20} color="#fff" />
        </ActionIcon>
        <ActionIcon
          variant="subtle"
          color="gray"
          aria-label={t('common.close')}
          onClick={onClose}
        >
          <IconX size={20} color="#fff" />
        </ActionIcon>
      </header>

      {many && index > 0 && (
        <ActionIcon
          className="pheme-viewer-prev"
          variant="filled"
          color="dark"
          radius="xl"
          size="lg"
          aria-label={t('channel.previousImage')}
          onClick={() => step(-1)}
        >
          <IconChevronLeft size={20} />
        </ActionIcon>
      )}
      {many && index < images.length - 1 && (
        <ActionIcon
          className="pheme-viewer-next"
          variant="filled"
          color="dark"
          radius="xl"
          size="lg"
          aria-label={t('channel.nextImage')}
          onClick={() => step(1)}
        >
          <IconChevronRight size={20} />
        </ActionIcon>
      )}

      <img
        className="pheme-viewer-img"
        src={imageUrl(image.id)}
        width={image.width}
        height={image.height}
        alt={t('channel.imageAlt', { index: index + 1, total: images.length })}
        data-zoomed={zoomed}
        draggable={false}
        style={{
          transform: `translate3d(${x}px, ${y}px, 0) scale(${scale})`,
          transition: dragging ? 'none' : 'transform 180ms ease',
        }}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
        onDoubleClick={toggleZoom}
      />
    </div>
  )

  // Portalled to the body: the viewer must escape the conversation pane, which is
  // a fixed-height, overflow-hidden grid cell.
  return createPortal(view, document.body)
}
