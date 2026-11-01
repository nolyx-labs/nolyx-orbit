export function setTheme(t: 'dark' | 'light') {
  document.documentElement.dataset.theme = t
  try {
    localStorage.setItem('orbit-theme', t)
  } catch {}
  window.dispatchEvent(new Event('orbit-theme'))
}
