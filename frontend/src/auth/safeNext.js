// cuma terima path internal ("/..."), bukan "//evil.com" atau "/\evil.com" biar ga jadi open redirect
export function safeNext(raw) {
  if (!raw || !raw.startsWith('/') || raw.startsWith('//') || raw.startsWith('/\\')) return '/'
  return raw
}
