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
  fontFamily: fontStack,
  fontFamilyMonospace: "'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace",
  headings: {
    fontFamily: "'Space Grotesk', " + fontStack,
    fontWeight: '600',
  },
  defaultRadius: 'md',
  components: {
    Card: { defaultProps: { radius: 'lg', withBorder: true, shadow: 'sm' } },
    Paper: { defaultProps: { radius: 'lg' } },
    Button: { defaultProps: { radius: 'md' } },
    ActionIcon: { defaultProps: { radius: 'md' } },
    Badge: { defaultProps: { radius: 'sm' } },
    TextInput: { defaultProps: { radius: 'md' } },
    PasswordInput: { defaultProps: { radius: 'md' } },
    Textarea: { defaultProps: { radius: 'md' } },
    Select: { defaultProps: { radius: 'md' } },
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
