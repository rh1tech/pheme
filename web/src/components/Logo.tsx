import { Group, Text, ThemeIcon } from '@mantine/core'
import { IconFeather } from '@tabler/icons-react'
import { BRAND_GRADIENT } from '../theme'

interface LogoProps {
  size?: 'sm' | 'lg'
  onClick?: () => void
}

// The Pheme wordmark: a gradient tile bearing a quill feather (messenger / the
// spreading of word and fame) alongside a gradient Space Grotesk wordmark.
export function Logo({ size = 'sm', onClick }: LogoProps) {
  const dim = size === 'lg' ? 40 : 32
  return (
    <Group
      gap={10}
      wrap="nowrap"
      style={{ cursor: onClick ? 'pointer' : undefined, userSelect: 'none' }}
      onClick={onClick}
    >
      <ThemeIcon variant="gradient" gradient={BRAND_GRADIENT} size={dim} radius="md">
        <IconFeather size={dim * 0.6} />
      </ThemeIcon>
      <Text
        fw={700}
        fz={size === 'lg' ? 30 : 22}
        lh={1}
        ff="'Space Grotesk', sans-serif"
        variant="gradient"
        gradient={BRAND_GRADIENT}
      >
        Pheme
      </Text>
    </Group>
  )
}
