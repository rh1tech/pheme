import { useEffect, useRef, useState } from 'react'
import { Box, Group } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { imageUrl } from '../lib/api'
import type { MessageImage } from '../lib/types'

interface ImageCarouselProps {
  images: MessageImage[]
  /** Opens the fullscreen viewer at the given image. Omit to leave images inert. */
  onOpen?: (index: number) => void
}

/**
 * Instagram-style image carousel: a horizontal scroll-snap strip with dot
 * indicators. Scrolling is native (compositor-friendly); the active dot is
 * tracked with an IntersectionObserver to avoid scroll-handler churn.
 */
export function ImageCarousel({ images, onOpen }: ImageCarouselProps) {
  const { t } = useTranslation()
  const trackRef = useRef<HTMLDivElement>(null)
  const [active, setActive] = useState(0)

  useEffect(() => {
    const track = trackRef.current
    if (!track || images.length < 2) return

    const slides = Array.from(track.querySelectorAll<HTMLElement>('[data-slide]'))
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            const idx = Number((entry.target as HTMLElement).dataset.slide)
            setActive(idx)
          }
        }
      },
      { root: track, threshold: 0.6 },
    )
    slides.forEach((s) => observer.observe(s))
    return () => observer.disconnect()
  }, [images.length])

  if (images.length === 0) return null

  function scrollTo(index: number) {
    const track = trackRef.current
    if (!track) return
    const slide = track.querySelector<HTMLElement>(`[data-slide="${index}"]`)
    slide?.scrollIntoView({ behavior: 'smooth', inline: 'center', block: 'nearest' })
  }

  // A DEFINITE pixel width, so each slide is a definite width and the browser reserves every image's
  // height from its width/height attributes BEFORE it lazy-loads. Without it the strip lived in a
  // shrink-to-fit bubble whose width is indefinite, so `width: 100%; height: auto` reserved nothing and
  // each image shoved the feed as it scrolled into view. `max-width: 100%` (below) clamps it on a
  // narrow screen; capped at the bubble's max so a huge image does not blow past it.
  const widest = Math.max(...images.map((img) => img.width || 0))
  const displayWidth = Math.min(widest > 0 ? widest : 320, 544)

  return (
    <Box style={{ width: displayWidth, maxWidth: '100%' }}>
      <div className="pheme-carousel" ref={trackRef}>
        {images.map((img, i) => (
          <div className="pheme-carousel-slide" data-slide={i} key={img.id}>
            <img
              src={imageUrl(img.id)}
              width={img.width}
              height={img.height}
              loading="lazy"
              decoding="async"
              alt={t('channel.imageAlt', { index: i + 1, total: images.length })}
              style={onOpen ? { cursor: 'zoom-in' } : undefined}
              role={onOpen ? 'button' : undefined}
              tabIndex={onOpen ? 0 : undefined}
              aria-label={onOpen ? t('channel.openImage') : undefined}
              onClick={onOpen ? () => onOpen(i) : undefined}
              onKeyDown={
                onOpen
                  ? (e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault()
                        onOpen(i)
                      }
                    }
                  : undefined
              }
            />
          </div>
        ))}
      </div>
      {images.length > 1 && (
        <Group justify="center" gap={6} mt={8}>
          {images.map((img, i) => (
            <button
              key={img.id}
              type="button"
              className="pheme-carousel-dot"
              data-active={i === active}
              aria-label={t('channel.goToImage', { index: i + 1 })}
              aria-current={i === active}
              onClick={() => scrollTo(i)}
            />
          ))}
        </Group>
      )}
    </Box>
  )
}
