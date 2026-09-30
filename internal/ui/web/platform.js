// Runs before first paint: pick the platform look (macOS, Windows, Linux).
(() => {
  const ua = navigator.userAgent;
  const platform = /Mac/.test(ua) ? 'macos' : /Windows/.test(ua) ? 'windows' : 'linux';
  document.documentElement.dataset.platform = platform;
})();
