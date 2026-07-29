// Apply the saved Mantine color scheme before first paint, to avoid a flash of
// the wrong theme.
//
// This lives in its own file rather than inline in index.html so the Content-
// Security-Policy can be `script-src 'self'` with no 'unsafe-inline' and no hash
// to keep in sync. A blocking <script src> in <head> runs at exactly the same
// point in the parse as the inline version did, so the anti-flash behaviour is
// unchanged.
(function () {
  try {
    var v = localStorage.getItem('mantine-color-scheme-value') || 'auto';
    var scheme =
      v === 'auto'
        ? window.matchMedia('(prefers-color-scheme: dark)').matches
          ? 'dark'
          : 'light'
        : v;
    document.documentElement.setAttribute('data-mantine-color-scheme', scheme);
  } catch (e) {
    /* ignore */
  }
})();
