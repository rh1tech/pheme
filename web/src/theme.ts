import { createTheme, type MantineColorsTuple } from '@mantine/core'

// "Iris" — a confident violet/indigo brand palette (light → dark), chosen to
// move away from the default Mantine blue and give Pheme a distinct identity.
const iris: MantineColorsTuple = [
  '#f4f0ff',
  '#e4dcff',
  '#c6b4fb',
  '#a888f5',
  '#8f63f0',
  '#7f4cee',
  '#7740ee',
  '#6531d4',
  '#592abe',
  '#4c22a8',
]

export const BRAND_GRADIENT = { from: 'iris.6', to: 'grape.6', deg: 135 }

const fontStack =
  "'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif"

export const theme = createTheme({
  primaryColor: 'iris',
  primaryShade: { light: 6, dark: 5 },
  colors: { iris },
  defaultGradient: BRAND_GRADIENT,
  autoContrast: true,
  cursorType: 'pointer',
  focusRing: 'auto',
  fontFamily: fontStack,
  fontFamilyMonospace: "'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace",
  headings: {
    fontFamily: "'Space Grotesk', " + fontStack,
    fontWeight: '600',
  },
  defaultRadius: 'md',
  shadows: {
    xs: '0 1px 2px rgba(20, 16, 40, 0.06)',
    sm: '0 2px 8px rgba(20, 16, 40, 0.08)',
    md: '0 8px 24px rgba(20, 16, 40, 0.10)',
    lg: '0 16px 40px rgba(20, 16, 40, 0.14)',
    xl: '0 24px 60px rgba(20, 16, 40, 0.18)',
  },
  components: {
    Card: { defaultProps: { radius: 'lg', withBorder: true, shadow: 'sm' } },
    Paper: { defaultProps: { radius: 'lg' } },
    Button: { defaultProps: { radius: 'md' } },
    ActionIcon: { defaultProps: { radius: 'md' } },
    Badge: { defaultProps: { radius: 'sm' } },
    TextInput: { defaultProps: { radius: 'md', variant: 'filled' } },
    PasswordInput: { defaultProps: { radius: 'md', variant: 'filled' } },
    Textarea: { defaultProps: { radius: 'md', variant: 'filled' } },
    Select: { defaultProps: { radius: 'md', variant: 'filled' } },
    Menu: { defaultProps: { radius: 'md', shadow: 'md' } },
    Modal: {
      defaultProps: {
        radius: 'lg',
        centered: true,
        overlayProps: { blur: 3, backgroundOpacity: 0.55 },
      },
    },
    Tooltip: { defaultProps: { radius: 'sm' } },
  },
})
