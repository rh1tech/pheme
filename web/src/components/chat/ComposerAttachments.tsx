import { useEffect, useMemo } from 'react'
import { CloseButton } from '@mantine/core'
import { useTranslation } from 'react-i18next'

interface ComposerAttachmentsProps {
  files: File[]
  onRemove: (index: number) => void
}

/**
 * A compact strip of square thumbnails above the composer. Attachments are a
 * footnote to the text being written: they must never grow to the width of the
 * pane and push the message box off screen.
 */
export function ComposerAttachments({ files, onRemove }: ComposerAttachmentsProps) {
  const { t } = useTranslation()
  const previews = useMemo(() => files.map((f) => URL.createObjectURL(f)), [files])
  useEffect(() => () => previews.forEach((url) => URL.revokeObjectURL(url)), [previews])

  if (files.length === 0) return null

  return (
    <div className="pheme-attachments">
      {files.map((file, i) => (
        <div className="pheme-attachment" key={`${file.name}-${i}`}>
          <img src={previews[i]} alt={file.name} />
          <CloseButton
            size="xs"
            radius="xl"
            variant="filled"
            color="dark"
            aria-label={t('channel.removeImage')}
            onClick={() => onRemove(i)}
          />
        </div>
      ))}
    </div>
  )
}
