export const locales = ['en', 'zh', 'zh-TW', 'ja', 'de'];

// Keep the language mapping aligned with app/frontend/src/i18n/core.ts.
export function resolveLocale(preference, system) {
  if (locales.includes(preference)) return preference;
  const tag = String(system || 'en').toLowerCase().replaceAll('_', '-');
  if (/^zh(?:-|$)/.test(tag)) return /(?:^|-)(?:tw|hk|mo|hant)(?:-|$)/.test(tag) ? 'zh-TW' : 'zh';
  if (/^ja(?:-|$)/.test(tag)) return 'ja';
  if (/^de(?:-|$)/.test(tag)) return 'de';
  return 'en';
}

export function resolveAppearance(preference, systemDark) {
  return preference === 'light' || preference === 'dark' ? preference : systemDark ? 'dark' : 'light';
}

export function watchAppearance(media, initialPreference, apply) {
  let preference = ['system', 'light', 'dark'].includes(initialPreference) ? initialPreference : 'system';
  const update = () => apply(resolveAppearance(preference, media.matches));
  const onChange = () => { if (preference === 'system') update(); };
  media.addEventListener('change', onChange);
  update();
  return {
    set(next) { preference = ['light', 'dark'].includes(next) ? next : 'system'; update(); },
    dispose() { media.removeEventListener('change', onChange); }
  };
}
