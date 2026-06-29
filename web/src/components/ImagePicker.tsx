import { useEffect, useMemo } from 'react'
import {
  Box,
  CloseButton,
  FileButton,
  Image,
  SimpleGrid,
  Stack,
  Text,
  UnstyledButton,
} from '@mantine/core'
import { IconPhoto } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'

interface ImagePickerProps {
  files: File[]
  max: number
  onAdd: (selected: File[]) => void
  onRemove: (index: number) => void
}

/**
 * A compact image picker: a square thumbnail grid with a remove control on each
 * preview and a dashed "add" tile that opens the file chooser (until `max` is
 * reached). Owns the object-URL previews for its `files`.
 */
export function ImagePicker({ files, max, onAdd, onRemove }: ImagePickerProps) {
  const { t } = useTranslation()
  const previews = useMemo(() => files.map((f) => URL.createObjectURL(f)), [files])
  useEffect(() => () => previews.forEach((u) => URL.revokeObjectURL(u)), [previews])

  return (
    <Stack gap={8}>
      <Text size="sm" fw={500}>
        {t('channel.images')}
      </Text>
      <SimpleGrid cols={{ base: 3, xs: 4 }} spacing="xs">
        {files.map((file, i) => (
          <Box
            key={`${file.name}-${i}`}
            pos="relative"
            style={{ aspectRatio: '1', borderRadius: 'var(--mantine-radius-md)', overflow: 'hidden' }}
          >
            <Image src={previews[i]} alt={file.name} h="100%" w="100%" fit="cover" />
            <CloseButton
              size="sm"
              radius="xl"
              variant="filled"
              color="dark"
              aria-label={t('channel.removeImage')}
              onClick={() => onRemove(i)}
              pos="absolute"
              top={4}
              right={4}
            />
          </Box>
        ))}
        {files.length < max && (
          <FileButton onChange={onAdd} accept="image/*" multiple>
            {(props) => (
              <UnstyledButton {...props} className="pheme-image-add" aria-label={t('channel.addImages')}>
                <IconPhoto size={22} stroke={1.6} />
                <Text size="xs">{t('channel.addImages')}</Text>
              </UnstyledButton>
            )}
          </FileButton>
        )}
      </SimpleGrid>
      <Text size="xs" c="dimmed">
        {t('channel.imagesHint', { max })}
      </Text>
    </Stack>
  )
}
