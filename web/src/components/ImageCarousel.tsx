import { useEffect, useRef, useState } from 'react'
import { Box, Group } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { imageUrl } from '../lib/api'
import type { MessageImage } from '../lib/types'

interface ImageCarouselProps {
  images: MessageImage[]
}

/**
 * Instagram-style image carousel: a horizontal scroll-snap strip with dot
 * indicators. Scrolling is native (compositor-friendly); the active dot is
 * tracked with an IntersectionObserver to avoid scroll-handler churn.
 */
export function ImageCarousel({ images }: ImageCarouselProps) {
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

  return (
    <Box>
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
