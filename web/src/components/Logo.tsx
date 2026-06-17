import { Group, Text, ThemeIcon } from '@mantine/core'
import { BRAND_GRADIENT } from '../theme'
import { PhemeMark } from './PhemeMark'

interface LogoProps {
  size?: 'sm' | 'lg'
  onClick?: () => void
}

// The Pheme wordmark: a gradient tile bearing the winged-messenger goddess mark
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
        <PhemeMark size={dim * 0.72} color="#fff" />
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
