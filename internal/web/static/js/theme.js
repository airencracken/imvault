// Apply the saved choice before CSS loads; CSS handles system preference changes.
(() => {
  const key = 'imvault-theme';
  const normalize = value => value === 'light' || value === 'dark' ? value : 'system';
  let preference = 'system';
  try {
    preference = normalize(localStorage.getItem(key));
  } catch { /* Storage may be disabled. The switch still works for this page. */ }

  function apply() {
    if (preference === 'system') document.documentElement.removeAttribute('data-theme');
    else document.documentElement.setAttribute('data-theme', preference);
  }

  function syncControls() {
    for (const control of document.querySelectorAll('[data-theme-select]')) {
      control.value = preference;
      control.closest('.theme-picker').hidden = false;
    }
  }

  apply();
  document.addEventListener('DOMContentLoaded', syncControls);
  document.addEventListener('htmx:afterSwap', syncControls);
  document.addEventListener('change', event => {
    if (!event.target.matches('[data-theme-select]')) return;
    preference = normalize(event.target.value);
    apply();
    syncControls();
    try {
      if (preference === 'system') localStorage.removeItem(key);
      else localStorage.setItem(key, preference);
    } catch { /* Keep the in-memory choice when storage is unavailable. */ }
  });
  window.addEventListener('storage', event => {
    if (event.key !== key && event.key !== null) return;
    preference = normalize(event.newValue);
    apply();
    syncControls();
  });
  window.addEventListener('pageshow', event => {
    if (!event.persisted) return;
    // Back/forward navigation can restore a page with an older preference.
    try {
      preference = normalize(localStorage.getItem(key));
    } catch { /* Preserve the page's choice when storage is unavailable. */ }
    apply();
    syncControls();
  });
})();
