import { useEffect, useRef, useState } from 'react'
import {
  ActionIcon,
  Avatar,
  Button,
  Card,
  Group,
  Stack,
  Text,
  Textarea,
  Title,
} from '@mantine/core'
import { IconTrash } from '@tabler/icons-react'
import { useTranslation } from 'react-i18next'
import { api, imageUrl } from '../lib/api'
import { notifyError, notifySuccess } from '../lib/notify'
import { useAuth } from '../auth/context'
import type { Comment } from '../lib/types'
import { ConfirmModal } from './ConfirmModal'

interface CommentsPanelProps {
  channelId: string
  messageId: string
  /** Whether this message permits comments (per-message flag). */
  commentsAllowed: boolean
  /** Whether the viewer is an active member (or owner) able to post. */
  canComment: boolean
  /** Whether the viewer can delete others' comments (owner / channel-admin). */
  canModerate: boolean
  /**
   * Put the cursor in the comment box once it renders. Set by the discussion pane:
   * the reader got there by clicking "Comment", so writing one is what they came
   * to do.
   */
  autoFocus?: boolean
}

function authorLabel(c: Comment, fallback: string): string {
  return c.author.displayName || c.author.username || fallback
}

export function CommentsPanel({
  channelId,
  messageId,
  commentsAllowed,
  canComment,
  canModerate,
  autoFocus = false,
}: CommentsPanelProps) {
  const { t } = useTranslation()
  const { userId } = useAuth()
  const [comments, setComments] = useState<Comment[] | null>(null)
  const [nextCursor, setNextCursor] = useState('')
  const [body, setBody] = useState('')
  const [posting, setPosting] = useState(false)
  const [removeId, setRemoveId] = useState<string | null>(null)
  const [removing, setRemoving] = useState(false)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  const canPost = commentsAllowed && canComment

  // Re-focus when another message's discussion opens in the same pane, not only
  // on first mount.
  useEffect(() => {
    if (!autoFocus || !canPost) return
    inputRef.current?.focus()
  }, [autoFocus, canPost, messageId])

  useEffect(() => {
    let active = true
    api
      .listComments(channelId, messageId)
      .then((p) => {
        if (!active) return
        setComments(p.comments)
        setNextCursor(p.nextCursor)
      })
      .catch((e) => active && notifyError(t('channel.comments.loadFailed'), e))
    return () => {
      active = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId, messageId])

  async function loadMore() {
    try {
      const p = await api.listComments(channelId, messageId, nextCursor)
      setComments((prev) => [...(prev ?? []), ...p.comments])
      setNextCursor(p.nextCursor)
    } catch (e) {
      notifyError(t('channel.comments.loadFailed'), e)
    }
  }

  async function post() {
    const text = body.trim()
    if (!text) return
    setPosting(true)
    try {
      const created = await api.postComment(channelId, messageId, text)
      setComments((prev) => [created, ...(prev ?? [])])
      setBody('')
      notifySuccess(t('channel.comments.posted'))
    } catch (e) {
      notifyError(t('channel.comments.postFailed'), e)
    } finally {
      setPosting(false)
    }
  }

  async function confirmRemove() {
    if (!removeId) return
    setRemoving(true)
    try {
      await api.deleteComment(channelId, messageId, removeId)
      setComments((prev) => (prev ?? []).filter((c) => c.id !== removeId))
      notifySuccess(t('channel.comments.deleted'))
      setRemoveId(null)
    } catch (e) {
      notifyError(t('channel.comments.deleteFailed'), e)
    } finally {
      setRemoving(false)
    }
  }

  return (
    <Stack gap="md">
      <Title order={5}>{t('channel.comments.title')}</Title>

      {canPost && (
        <Group align="flex-end" gap="sm" wrap="nowrap">
          <Textarea
            ref={inputRef}
            style={{ flex: 1 }}
            autosize
            minRows={1}
            maxRows={4}
            aria-label={t('channel.comments.placeholder')}
            placeholder={t('channel.comments.placeholder')}
            value={body}
            onChange={(e) => setBody(e.currentTarget.value)}
          />
          <Button onClick={post} loading={posting} disabled={body.trim() === ''}>
            {t('channel.comments.post')}
          </Button>
        </Group>
      )}
      {!commentsAllowed && (
        <Text c="dimmed" size="sm">
          {t('channel.comments.closed')}
        </Text>
      )}
      {commentsAllowed && !canComment && (
        <Text c="dimmed" size="sm">
          {t('channel.comments.joinToComment')}
        </Text>
      )}

      {comments && comments.length === 0 && (
        <Text c="dimmed" size="sm">
          {t('channel.comments.empty')}
        </Text>
      )}

      <Stack gap="sm">
        {(comments ?? []).map((c) => (
          <Card key={c.id} withBorder padding="sm" radius="md">
            <Group justify="space-between" align="flex-start" wrap="nowrap">
              <Group gap="sm" align="flex-start" wrap="nowrap">
                <Avatar
                  src={c.author.avatarId ? imageUrl(c.author.avatarId) : undefined}
                  size={32}
                  radius="xl"
                  color="iris"
                >
                  {authorLabel(c, t('channel.comments.anonymous')).slice(0, 2).toUpperCase()}
                </Avatar>
                <Stack gap={2}>
                  <Group gap="xs">
                    <Text size="sm" fw={600}>
                      {authorLabel(c, t('channel.comments.anonymous'))}
                    </Text>
                    <Text size="xs" c="dimmed">
                      {new Date(c.createdAt).toLocaleString()}
                    </Text>
                  </Group>
                  <Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>
                    {c.body}
                  </Text>
                </Stack>
              </Group>
              {(canModerate || c.userId === userId) && (
                <ActionIcon
                  variant="subtle"
                  color="red"
                  aria-label={t('channel.comments.delete')}
                  onClick={() => setRemoveId(c.id)}
                >
                  <IconTrash size={16} />
                </ActionIcon>
              )}
            </Group>
          </Card>
        ))}
      </Stack>

      {nextCursor && (
        <Group justify="center">
          <Button variant="subtle" onClick={loadMore}>
            {t('channel.comments.loadMore')}
          </Button>
        </Group>
      )}

      <ConfirmModal
        opened={removeId !== null}
        onClose={() => setRemoveId(null)}
        onConfirm={confirmRemove}
        title={t('channel.comments.delete')}
        loading={removing}
      >
        <Text size="sm">{t('channel.comments.deleteConfirm')}</Text>
      </ConfirmModal>
    </Stack>
  )
}
