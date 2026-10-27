/** App mark: a ship's helm wheel ("leme") on an ocean gradient; glyph from Lucide "ship-wheel" (ISC). See app/build/icon.svg. */
export function Logo({ className = 'brand-logo' }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 64 64" aria-hidden="true">
      <defs>
        <linearGradient id="leme-logo" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#0ea5e9" />
          <stop offset="1" stopColor="#1e3a8a" />
        </linearGradient>
      </defs>
      <rect width="64" height="64" rx="14" fill="url(#leme-logo)" />
      <g transform="translate(8 8) scale(2)" fill="none" stroke="#fff" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
        <circle cx="12" cy="12" r="8" />
        <path d="M12 2v7.5" />
        <path d="m19 5-5.23 5.23" />
        <path d="M22 12h-7.5" />
        <path d="m19 19-5.23-5.23" />
        <path d="M12 14.5V22" />
        <path d="M10.23 13.77 5 19" />
        <path d="M9.5 12H2" />
        <path d="M10.23 10.23 5 5" />
        <circle cx="12" cy="12" r="2.5" fill="#fff" fillOpacity="0.25" />
      </g>
    </svg>
  )
}
