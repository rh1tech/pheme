import { Card, Group, Skeleton, Stack, Table } from '@mantine/core'

/** A list of card-shaped placeholders shown while list data loads. */
export function CardListSkeleton({ rows = 4 }: { rows?: number }) {
  return (
    <Stack gap="sm">
      {Array.from({ length: rows }).map((_, i) => (
        <Card key={i} withBorder padding="md">
          <Group justify="space-between" align="flex-start">
            <Stack gap={8} style={{ flex: 1 }}>
              <Skeleton height={14} width="40%" radius="sm" />
              <Skeleton height={10} width="65%" radius="sm" />
            </Stack>
            <Skeleton height={24} width={72} radius="sm" />
          </Group>
        </Card>
      ))}
    </Stack>
  )
}

/** Placeholder rows matching a table layout while it loads. */
export function TableRowsSkeleton({ rows = 6, cols = 4 }: { rows?: number; cols?: number }) {
  return (
    <Table.Tbody>
      {Array.from({ length: rows }).map((_, r) => (
        <Table.Tr key={r}>
          {Array.from({ length: cols }).map((_, c) => (
            <Table.Td key={c}>
              <Skeleton height={12} radius="sm" width={c === 0 ? '70%' : '50%'} />
            </Table.Td>
          ))}
        </Table.Tr>
      ))}
    </Table.Tbody>
  )
}
