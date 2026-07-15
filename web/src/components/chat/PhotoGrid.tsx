// Photos inside a message bubble.
//
// A photo cannot be shown by pointing an <img> at a URL: there is nothing at that URL but sealed
// bytes, and the key never leaves this tab. So each one is fetched as ciphertext, opened with the key
// that came inside the message, and turned into an object URL for the browser to draw.
//
// The space is reserved from the FIRST render, using the dimensions carried in the message. A bubble
// that does not know how tall a photo will be has to guess, and when the bytes land and the guess was
// wrong the whole feed jumps under the reader. Knowing the shape in advance is the difference between
// a photo appearing and a photo shoving.

import { useEffect, useState } from 'react'
import { Box, Image, Modal } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { IconPhotoOff } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'

import { loadPhotoUrl } from '../../lib/photoCache'
import type { ChatPhoto } from '../../lib/chatContent'

interface PhotoGridProps {
  conversationId: string
  photos: ChatPhoto[]
}

export function PhotoGrid({ conversationId, photos }: PhotoGridProps) {
  if (photos.length === 0) return null

  // One photo keeps its own shape. Several are squared off into a grid, because a row of wildly
  // different aspect ratios reads as clutter rather than as a set.
  const grid = photos.length > 1

  return (
    <div className="pheme-photo-grid" data-grid={grid}>
      {photos.map((photo) => (
        <Photo
          key={photo.id}
          conversationId={conversationId}
          photo={photo}
          square={grid}
        />
      ))}
    </div>
  )
}

function Photo({
  conversationId,
  photo,
  square,
}: {
  conversationId: string
  photo: ChatPhoto
  square: boolean
}) {
  const { t } = useTranslation()
  const [url, setUrl] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)
  const [opened, { open, close }] = useDisclosure(false)

  useEffect(() => {
    let active = true
    // The decrypted bytes are cached (lib/photoCache), so a photo scrolled back into view — or the
    // whole chat reopened — shows instantly from cache instead of re-fetching and blinking. The cache
    // owns the object URL's lifetime and bounds how many are kept, so this effect must NOT revoke on
    // unmount: revoking a URL another mounted <img> is still pointing at would blank it.
    loadPhotoUrl(conversationId, photo.id, photo.key, photo.mime)
      .then((objectUrl) => {
        if (active) setUrl(objectUrl)
      })
      .catch(() => {
        // A photo that will not open is not the photo that was sent, and showing something else would
        // be worse than saying so. It is also permanent — the key is in a message this device cannot
        // read — so this must not look like a retry is coming.
        if (active) setFailed(true)
      })

    return () => {
      active = false
    }
  }, [conversationId, photo.id, photo.key, photo.mime])

  // The aspect ratio comes from the message, so the box is the right shape before the bytes arrive.
  const ratio = square ? 1 : photo.h > 0 ? photo.w / photo.h : 1

  return (
    <>
      <Box
        className="pheme-photo"
        style={{ aspectRatio: String(ratio) }}
        onClick={url ? open : undefined}
        data-clickable={Boolean(url)}
      >
        {failed ? (
          <IconPhotoOff size={20} stroke={1.6} aria-label={t('chat.photoUnavailable')} />
        ) : url ? (
          <Image src={url} alt={t('chat.photo')} fit={square ? 'cover' : 'contain'} />
        ) : null}
      </Box>

      <Modal opened={opened} onClose={close} size="auto" centered withCloseButton={false} padding={0}>
        {url && <Image src={url} alt={t('chat.photo')} fit="contain" mah="85vh" />}
      </Modal>
    </>
  )
}
