import { Group, Text, ThemeIcon } from '@mantine/core'
import { IconSpeakerphone } from '@tabler/icons-react'
import { BRAND_GRADIENT } from '../theme'

interface LogoProps {
  size?: 'sm' | 'lg'
  onClick?: () => void
}

// The Pheme wordmark: a gradient "broadcast" badge alongside a gradient
// Space Grotesk wordmark. Used in the header and on the login screen.
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
        <IconSpeakerphone size={dim * 0.62} />
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
