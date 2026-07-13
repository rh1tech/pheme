import { Skeleton, Stack } from '@mantine/core'

// Bubble-shaped placeholders with varied widths, so a loading feed reads as
// messages rather than as a table.
const WIDTHS = ['60%', '45%', '72%', '38%', '55%']

export function ChatSkeleton() {
  return (
    <Stack gap="xs" py="xs">
      {WIDTHS.map((w, i) => (
        <Skeleton key={i} height={52} radius="lg" width={w} />
      ))}
    </Stack>
  )
}
