/** App mark: an isometric cube with a status dot (original artwork, see app/build/icon.svg). */
export function Logo({ className = 'brand-logo' }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 64 64" aria-hidden="true">
      <defs>
        <linearGradient id="k8st-logo" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#3b82f6" />
          <stop offset="1" stopColor="#7c3aed" />
        </linearGradient>
      </defs>
      <rect width="64" height="64" rx="14" fill="url(#k8st-logo)" />
      <polygon points="32,13 49,22.5 32,32 15,22.5" fill="#fff" fillOpacity="0.96" />
      <polygon points="15,22.5 32,32 32,51 15,41.5" fill="#fff" fillOpacity="0.68" />
      <polygon points="49,22.5 49,41.5 32,51 32,32" fill="#fff" fillOpacity="0.42" />
      <circle cx="47" cy="45" r="7" fill="#22c55e" stroke="#fff" strokeWidth="2.5" />
    </svg>
  )
}
