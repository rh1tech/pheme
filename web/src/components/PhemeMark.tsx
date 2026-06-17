interface PhemeMarkProps {
  size?: number
  color?: string
}

/**
 * The Pheme brand mark: an original, stylised silhouette of a winged messenger
 * goddess (Pheme — the Greek goddess of fame and report). Upswept wings rise
 * behind a head and a flowing gown. Drawn in a single colour so it sits on the
 * gradient tile in the Logo and stays legible at favicon sizes.
 */
export function PhemeMark({ size = 64, color = 'currentColor' }: PhemeMarkProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 64 64"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
    >
      <g fill={color}>
        {/* Upswept wings rising behind the shoulders. */}
        <path d="M30 24C26 14 18 6 8 4c4 6 6 11 6 16-3-3-6-5-10-5 5 3 8 7 10 12 2 4 5 5 8 2 2-2 2-4-2-3z" />
        <path d="M34 24c4-10 12-18 22-20-4 6-6 11-6 16 3-3 6-5 10-5-5 3-8 7-10 12-2 4-5 5-8 2-2-2-2-4 2-3z" />
        {/* Head. */}
        <circle cx="32" cy="16" r="4.8" />
        {/* Flowing gown, tapering from the shoulders to a soft bell hem. */}
        <path d="M32 21c3.2 0 5.4 2.1 6.2 5.3l5 24.4c.5 2.3-1.3 4.1-3.3 3.2-2.4-1-5-1.6-7.9-1.6s-5.5.6-7.9 1.6c-2 .9-3.8-.9-3.3-3.2l5-24.4C26.6 23.1 28.8 21 32 21z" />
      </g>
    </svg>
  )
}
