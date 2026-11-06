interface Props {
  name: string
  url: string | null
  size?: number
}

/** Profile picture with an initials fallback. */
export function Avatar({ name, url, size = 28 }: Props) {
  const style = { width: size, height: size, fontSize: size * 0.42 }
  if (url) {
    return (
      <img
        className="avatar"
        src={url}
        alt=""
        style={style}
        // Google profile images may refuse requests that carry a Referer.
        referrerPolicy="no-referrer"
      />
    )
  }
  const initials = name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0]?.toUpperCase())
    .join('')
  return (
    <span className="avatar avatar-initials" style={style} aria-hidden="true">
      {initials || '?'}
    </span>
  )
}
