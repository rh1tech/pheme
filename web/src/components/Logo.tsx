import { Group, Text, ThemeIcon } from '@mantine/core'
import { IconBroadcast } from '@tabler/icons-react'
import { BRAND_GRADIENT } from '../theme'

interface LogoProps {
  size?: 'sm' | 'lg'
  onClick?: () => void
}

// The Pheme wordmark: a gradient tile bearing a broadcast/sound-wave glyph,
// tilted diagonally so it reads distinctly from Apple's upright Podcasts mark,
// alongside a gradient Space Grotesk wordmark.
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
        <IconBroadcast size={dim * 0.64} stroke={2.2} style={{ transform: 'rotate(-45deg)' }} />
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
