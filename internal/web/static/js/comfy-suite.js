/* SPDX-License-Identifier: AGPL-3.0-or-later */
(() => {
  'use strict';
  const root = document.documentElement;
  const key = 'comfyware:' + (root.dataset.comfyApp || 'app') + ':density';
  const valid = value => value === 'compact' || value === 'comfortable';
  const apply = value => {
    root.dataset.density = valid(value) ? value : 'comfortable';
    document.querySelectorAll('[data-density-controls]').forEach(control => { control.hidden = false; });
    document.querySelectorAll('[data-density-choice]').forEach(button => {
      button.setAttribute('aria-pressed', String(button.dataset.densityChoice === root.dataset.density));
    });
  };
  try { apply(localStorage.getItem(key)); } catch { apply('comfortable'); }
  document.addEventListener('click', event => {
    const button = event.target.closest('[data-density-choice]');
    if (button && valid(button.dataset.densityChoice)) {
      apply(button.dataset.densityChoice);
      try { localStorage.setItem(key, root.dataset.density); } catch { /* A preference remains usable without storage. */ }
    }
    document.querySelectorAll('.comfy-menu[open]').forEach(menu => {
      if (!menu.contains(event.target) || event.target.closest('a')) menu.open = false;
    });
  });
  document.addEventListener('change', event => {
    if (event.target.closest('.theme-picker')) document.querySelectorAll('.comfy-menu[open]').forEach(menu => { menu.open = false; });
  });
  document.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    document.querySelectorAll('.comfy-menu[open]').forEach(menu => {
      menu.open = false;
      menu.querySelector('summary').focus();
    });
  });
  document.addEventListener('DOMContentLoaded', () => apply(root.dataset.density));
  document.addEventListener('htmx:afterSwap', () => apply(root.dataset.density));
  window.addEventListener('storage', event => { if (event.key === key) apply(event.newValue); });
})();
