// Read before application stores, router guards or API startup requests. An
// OAuth callback code must not become a same-origin request's Referer value.
let callback = ''
if (window.location.pathname.startsWith('/plugin-login/callback/')) {
  if (window.location.search && window.location.href.length <= 64 * 1024) callback = window.location.href
  const meta = document.createElement('meta')
  meta.name = 'referrer'
  meta.content = 'no-referrer'
  document.head.appendChild(meta)
  window.history.replaceState(window.history.state, '', window.location.pathname)
}

export function takePluginLoginCallback() {
  const value = callback
  callback = ''
  return value
}
