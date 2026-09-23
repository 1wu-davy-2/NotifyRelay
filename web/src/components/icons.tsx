/**
 * The icon set.
 *
 * Inline SVG rather than a font or a sprite sheet, for the reasons the
 * server-rendered templates already give: the CSP is `img-src 'self'` with no
 * font-src, so an icon font would fall back to default-src and an external
 * sprite would not load at all — and a local one is a second request per icon
 * or a build step, for about forty lines of markup.
 *
 * The paths were traced from the server-rendered layout the interface replaced
 * rather than redrawn on a 24 grid, so that the sidebar looks the same to
 * somebody who has used this service for a year. That layout is gone now; these
 * are the icons, and a new one is drawn here.
 *
 * Each is stroke-only and inherits currentColor, so it follows the link it sits
 * in without a second rule.
 */

import type { ReactNode, SVGProps } from 'react'

interface IconProps extends SVGProps<SVGSVGElement> {
  /** Defaults to 16, the size every sidebar and button icon is drawn at. */
  size?: number
}

function Icon({ size = 16, children, ...rest }: IconProps & { children: ReactNode }) {
  return (
    <svg
      viewBox="0 0 16 16"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.4}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      {...rest}
    >
      {children}
    </svg>
  )
}

export const IconChannels = (p: IconProps) => (
  <Icon {...p}>
    <circle cx="8" cy="11" r="1.6" />
    <path d="M4.8 7.8a4.5 4.5 0 0 1 6.4 0M2.6 5.4a7.7 7.7 0 0 1 10.8 0" />
  </Icon>
)

export const IconDeliveries = (p: IconProps) => (
  <Icon {...p}>
    <path d="M14 2 7.2 8.8M14 2l-4.4 12-2.4-5.2L2 6.4 14 2z" />
  </Icon>
)

export const IconAudit = (p: IconProps) => (
  <Icon {...p}>
    <circle cx="8" cy="8" r="6" />
    <path d="M8 4.4V8l2.4 1.6" />
  </Icon>
)

export const IconKeys = (p: IconProps) => (
  <Icon {...p}>
    <circle cx="5.6" cy="10.4" r="2.6" />
    <path d="M7.6 8.4 13.4 2.6M11 5l1.8 1.8" />
  </Icon>
)

export const IconAPI = (p: IconProps) => (
  <Icon {...p}>
    <path d="M5.8 3.6 2.4 8l3.4 4.4M10.2 3.6 13.6 8l-3.4 4.4" />
  </Icon>
)

export const IconStart = (p: IconProps) => (
  <Icon {...p}>
    <path d="M3.4 2.2v11.6M3.4 3h7.4l-1.2 2.4 1.2 2.4H3.4" />
  </Icon>
)

/** The brand mark. Drawn a size up because it sits in a filled tile. */
export const IconLogo = (p: IconProps) => (
  <Icon size={18} strokeWidth={1.5} {...p}>
    <circle cx="8" cy="12.2" r="1.8" />
    <path d="M4.4 8.4a5.1 5.1 0 0 1 7.2 0M1.8 5.6a9 9 0 0 1 12.4 0" />
  </Icon>
)

/* ------------------------------------------------------------ theme toggle */

export const IconMoon = (p: IconProps) => (
  <Icon size={15} {...p}>
    <path d="M13.4 9.4A6.4 6.4 0 0 1 6.6 2.6a6.4 6.4 0 1 0 6.8 6.8z" />
  </Icon>
)

export const IconSun = (p: IconProps) => (
  <Icon size={15} {...p}>
    <circle cx="8" cy="8" r="3" />
    <path d="M8 1.4v1.6M8 13v1.6M3.4 3.4l1.1 1.1M11.5 11.5l1.1 1.1M1.4 8h1.6M13 8h1.6M3.4 12.6l1.1-1.1M11.5 4.5l1.1-1.1" />
  </Icon>
)

/* ------------------------------------------------------------- account menu */

export const IconLock = (p: IconProps) => (
  <Icon size={14} {...p}>
    <rect x="3" y="7" width="10" height="7" rx="1.5" />
    <path d="M5.4 7V4.8a2.6 2.6 0 0 1 5.2 0V7" />
  </Icon>
)

export const IconSignOut = (p: IconProps) => (
  <Icon size={14} {...p}>
    <path d="M6.4 14H3.6A1.6 1.6 0 0 1 2 12.4V3.6A1.6 1.6 0 0 1 3.6 2h2.8" />
    <path d="m10.4 11.2 3.2-3.2-3.2-3.2M13.6 8H6.4" />
  </Icon>
)

export const IconCaret = (p: IconProps) => (
  <Icon size={12} strokeWidth={2} {...p}>
    <path d="m4 6 4 4 4-4" />
  </Icon>
)
