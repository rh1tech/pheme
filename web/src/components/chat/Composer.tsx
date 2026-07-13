import { useEffect, useRef, useState } from 'react'
import type { ClipboardEvent, DragEvent, KeyboardEvent } from 'react'
import { ActionIcon, Group, Menu, Stack, Switch, Textarea, Tooltip } from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import { IconPaperclip, IconSend, IconSettings } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { ComposerAttachments } from './ComposerAttachments'
import { api } from '../../lib/api'
import { splitMessage } from '../../lib/message'
import { notifyError, notifySuccess } from '../../lib/notify'

// Keep these in sync with the server limits (internal/channel/notify_input.go).
const MAX_IMAGES = 10
const MAX_IMAGE_BYTES = 10 * 1024 * 1024

interface ComposerProps {
  channelId: string
  /**
   * Changes whenever a channel is picked in the sidebar. The channel id cannot
   * carry this on its own: re-picking the channel already open changes no id, and
   * the cursor should still land in the message box.
   */
  focusSignal: number
  /** Called after a successful send, so the feed can reconcile if the stream is slow. */
  onSent: () => void
}

export function Composer({ channelId, focusSignal, onSent }: ComposerProps) {
  const { t } = useTranslation()
  const isMobile = useMediaQuery('(max-width: 48em)')
  const [text, setText] = useState('')
  const [images, setImages] = useState<File[]>([])
  const [allowComments, setAllowComments] = useState(true)
  const [sending, setSending] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)
  const textRef = useRef<HTMLTextAreaElement>(null)
  const [dropping, setDropping] = useState(false)
  // Drag events fire for every child element the pointer crosses, so a plain
  // boolean flickers. Counting enter/leave pairs is the only stable way to know
  // whether the pointer is still anywhere inside the composer.
  const dragDepth = useRef(0)

  const canSend = text.trim().length > 0 || images.length > 0

  // Opening a channel puts the cursor in the message box, so typing just works.
  // Keyed on the channel (covers a deep link or reload) and on focusSignal
  // (covers picking a channel in the sidebar, even the one already open).
  // Not on a phone: focusing there throws up the keyboard over the conversation
  // the reader came to read.
  useEffect(() => {
    if (isMobile) return
    textRef.current?.focus()
  }, [channelId, focusSignal, isMobile])

  function addImages(selected: File[]) {
    if (selected.length === 0) return
    const tooBig = selected.find((f) => f.size > MAX_IMAGE_BYTES)
    if (tooBig) {
      notifyError(t('channel.imageTooLarge', { name: tooBig.name }))
      return
    }
    setImages((prev) => {
      const next = [...prev, ...selected]
      if (next.length > MAX_IMAGES) {
        notifyError(t('channel.tooManyImages', { max: MAX_IMAGES }))
        return next.slice(0, MAX_IMAGES)
      }
      return next
    })
  }

  function removeImage(index: number) {
    setImages((prev) => prev.filter((_, i) => i !== index))
  }

  /** Only images: a dragged .zip or a copied block of text is not an attachment. */
  function imagesFrom(items: DataTransferItemList): File[] {
    const files: File[] = []
    for (const item of Array.from(items)) {
      if (item.kind !== 'file' || !item.type.startsWith('image/')) continue
      const file = item.getAsFile()
      if (file) files.push(file)
    }
    return files
  }

  // Pasting a screenshot straight into the message box is how most images get
  // shared; making people save the file first and pick it from a dialog is busywork.
  function onPaste(e: ClipboardEvent<HTMLTextAreaElement>) {
    const pasted = imagesFrom(e.clipboardData.items)
    if (pasted.length === 0) return // plain text: let it paste normally
    e.preventDefault()
    addImages(pasted)
  }

  function onDragEnter(e: DragEvent) {
    if (!Array.from(e.dataTransfer.types).includes('Files')) return
    dragDepth.current += 1
    setDropping(true)
  }

  function onDragLeave() {
    dragDepth.current = Math.max(0, dragDepth.current - 1)
    if (dragDepth.current === 0) setDropping(false)
  }

  function onDrop(e: DragEvent) {
    e.preventDefault()
    dragDepth.current = 0
    setDropping(false)
    addImages(imagesFrom(e.dataTransfer.items))
  }

  async function send() {
    if (!canSend || sending) return
    setSending(true)
    try {
      // The title is the message's first sentence — the composer does not ask for
      // one separately.
      const { title, body } = splitMessage(text)
      await api.notifyChannel(channelId, title, body, images, allowComments)
      setText('')
      setImages([])
      setAllowComments(true)
      notifySuccess(t('channel.messageSent'))
      // The API answers 202: the dispatcher writes the message, which reaches us
      // over the live stream. onSent lets the feed backstop a stalled stream.
      onSent()
      textRef.current?.focus()
    } catch (e) {
      notifyError(t('channel.sendFailed'), e)
    } finally {
      setSending(false)
    }
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key !== 'Enter' || e.shiftKey) return
    // Enter commits an IME candidate; sending here would ship a half-typed word.
    if (e.nativeEvent.isComposing) return
    // On a phone the on-screen Enter is a newline — sending is the button's job.
    if (isMobile) return
    e.preventDefault()
    void send()
  }

  return (
    <div
      className="pheme-composer"
      data-testid="composer"
      data-dropping={dropping}
      onDragEnter={onDragEnter}
      onDragOver={(e) => e.preventDefault()}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
    >
      {dropping && (
        <div className="pheme-composer-drop" aria-hidden>
          {t('channel.dropImages')}
        </div>
      )}
      <Stack gap="xs">
        <ComposerAttachments files={images} onRemove={removeImage} />

        <Group gap="xs" align="flex-end" wrap="nowrap">
          <Tooltip label={t('channel.attachImages')} withArrow>
            <ActionIcon
              aria-label={t('channel.attachImages')}
              variant="subtle"
              color="gray"
              size="lg"
              onClick={() => fileRef.current?.click()}
            >
              <IconPaperclip size={20} />
            </ActionIcon>
          </Tooltip>
          <input
            ref={fileRef}
            type="file"
            accept="image/*"
            multiple
            hidden
            onChange={(e) => {
              addImages(Array.from(e.currentTarget.files ?? []))
              e.currentTarget.value = ''
            }}
          />

          {/* Per-message options live behind this, rather than as controls sitting
              permanently under the message box eating its height. */}
          <Menu position="top-start" width={240} shadow="md" closeOnItemClick={false}>
            <Menu.Target>
              <Tooltip label={t('channel.messageOptions')} withArrow>
                <ActionIcon
                  aria-label={t('channel.messageOptions')}
                  variant="subtle"
                  color="gray"
                  size="lg"
                >
                  <IconSettings size={20} />
                </ActionIcon>
              </Tooltip>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Label>{t('channel.messageOptions')}</Menu.Label>
              <div style={{ padding: '0.25rem 0.75rem 0.5rem' }}>
                <Switch
                  size="sm"
                  checked={allowComments}
                  onChange={(e) => setAllowComments(e.currentTarget.checked)}
                  label={t('channel.allowComments')}
                />
              </div>
            </Menu.Dropdown>
          </Menu>

          <Textarea
            ref={textRef}
            aria-label={t('channel.body')}
            placeholder={t('channel.composerPlaceholder')}
            data-testid="composer-body"
            autosize
            minRows={1}
            maxRows={8}
            value={text}
            onChange={(e) => setText(e.currentTarget.value)}
            onKeyDown={onKeyDown}
            onPaste={onPaste}
            style={{ flex: 1 }}
          />

          <ActionIcon
            aria-label={t('channel.send')}
            variant="filled"
            size="lg"
            loading={sending}
            disabled={!canSend}
            onClick={send}
          >
            <IconSend size={18} />
          </ActionIcon>
        </Group>
      </Stack>
    </div>
  )
}
