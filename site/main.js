import { locales, resolveLocale, watchAppearance } from './preferences.mjs';
import { translations } from './translations.mjs?v=0.1.0-review-20261018-c';
import { createTypewriter, cycleAgentLogos, createDialogue } from './motion.mjs?v=0.1.0-review-20261018-c';
import { publication, releaseAssets, downloadURL, matchingAsset } from './publication.mjs?v=0.1.0-review-20261018-c';
import { detectSystem } from './detection.mjs';

const languageSelect = document.getElementById('language-select');
const appearanceSelect = document.getElementById('appearance-select');
const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');
const typewriter = createTypewriter(document.getElementById('hero-typed'), reducedMotion);
cycleAgentLogos([...document.querySelectorAll('.hero-agent img')], reducedMotion);
const dialogue = createDialogue(['cli-request-typed', 'cli-response-typed', 'cli-instruction-typed', 'cli-command-typed'].map(id => document.getElementById(id)), reducedMotion);
const releaseSelect = document.getElementById('release-asset');
const releaseLink = document.getElementById('release-download');
const installPanel = document.getElementById('cli-install');
const installCommand = document.getElementById('cli-install-command');
const installCopy = document.getElementById('cli-install-copy');
const installFeedback = document.getElementById('cli-install-feedback');
let activeProduct = 'app';
let detectedSystem = null;
const productChoices = new Map();
let releaseManuallySelected = false;
function renderAssets() {
  const placeholder = document.createElement('option');
  placeholder.value = '';
  placeholder.disabled = true;
  placeholder.textContent = translations[activeLocale].downloadChoose;
  releaseSelect.replaceChildren(placeholder);
  for (const asset of releaseAssets.filter(asset => asset.product === activeProduct)) {
    const option = document.createElement('option');
    option.value = asset.id;
    option.textContent = asset.label;
    releaseSelect.append(option);
  }
  releaseSelect.value = productChoices.get(activeProduct) || matchingAsset(detectedSystem, activeProduct) || '';
  for (const button of document.querySelectorAll('[data-product]')) button.setAttribute('aria-pressed', String(button.dataset.product === activeProduct));
}
for (const button of document.querySelectorAll('[data-product]')) button.addEventListener('click', () => {
  activeProduct = button.dataset.product;
  renderAssets();
  renderDownload();
});
function renderDownload(text = translations[activeLocale]) {
  const url = downloadURL(releaseSelect.value);
  releaseLink.textContent = url ? text.download : publication.published ? text.downloadChoose : text.downloadPending;
  if (url) { releaseLink.href = url; releaseLink.removeAttribute('aria-disabled'); }
  else { releaseLink.removeAttribute('href'); releaseLink.setAttribute('aria-disabled', 'true'); }
  installPanel.hidden = activeProduct !== 'cli' || !releaseSelect.value;
  installCommand.textContent = releaseSelect.value.startsWith('cli-windows-')
    ? 'irm https://kotoai.site/install.ps1 | iex'
    : 'curl -fsSL https://kotoai.site/install.sh | sh';
  installCopy.title = text.cliInstallCopy;
  installCopy.setAttribute('aria-label', text.cliInstallCopy);
  installFeedback.textContent = '';
}
installCopy.addEventListener('click', async () => {
  const command = installCommand.textContent;
  try {
    await navigator.clipboard.writeText(command);
    if (installCommand.textContent === command && !installPanel.hidden) installFeedback.textContent = translations[activeLocale].cliInstallCopied;
  } catch {
    installFeedback.textContent = translations[activeLocale].cliInstallSelect;
    const range = document.createRange();
    range.selectNodeContents(installCommand);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
  }
});
releaseSelect.addEventListener('change', () => { releaseManuallySelected = true; productChoices.set(activeProduct, releaseSelect.value); renderDownload(); });
const screenshots = [...document.querySelectorAll('.screenshot')];
// Refresh this content revision whenever screenshot assets are replaced.
const screenshotRevision = 'cbac5d2e3ea0';
let activeLocale = 'en';
let activeTheme = document.documentElement.dataset.theme || 'light';
function renderScreenshots() {
  for (const image of screenshots) {
    const source = `assets/screenshots/${activeLocale}-${activeTheme}-${image.dataset.screen}.png?v=${screenshotRevision}`;
    const placeholder = image.parentElement.querySelector('.screenshot-placeholder');
    const loaded = () => { image.hidden = false; placeholder.hidden = true; };
    const failed = () => { image.hidden = true; placeholder.hidden = false; };
    image.onload = loaded;
    image.onerror = failed;
    if (image.getAttribute('src') !== source) { image.hidden = true; placeholder.hidden = false; image.src = source; }
    else if (image.complete) { if (image.naturalWidth) loaded(); else failed(); }
  }
}
const tabs = [...document.querySelectorAll('[role="tab"]')];
const storage = {
  read(key, fallback) { try { return localStorage.getItem(key) || fallback; } catch { return fallback; } },
  write(key, value) { try { localStorage.setItem(key, value); } catch { /* Preferences remain usable without storage. */ } }
};
let languagePreference = storage.read('koto-site-language', 'system');
if (!locales.includes(languagePreference)) languagePreference = 'system';
let appearancePreference = storage.read('koto-site-appearance', 'system');
if (!['system', 'light', 'dark'].includes(appearancePreference)) appearancePreference = 'system';

function renderAppearanceControl() {
  for (const icon of document.querySelectorAll('[data-appearance]')) icon.toggleAttribute('hidden', icon.dataset.appearance !== appearancePreference);
  document.querySelector('.appearance-control').title = translations[activeLocale].appearance;
}

function setLanguage(preference) {
  languagePreference = locales.includes(preference) ? preference : 'system';
  const locale = resolveLocale(languagePreference, navigator.language);
  const text = translations[locale];
  activeLocale = locale;
  document.documentElement.lang = locale === 'zh' ? 'zh-CN' : locale;
  for (const element of document.querySelectorAll('[data-i18n]')) element.textContent = text[element.dataset.i18n];
  document.querySelector('[data-i18n=hero1Prefix]').hidden = !text.hero1Prefix;
  document.querySelector('[data-i18n=hero1Suffix]').hidden = !text.hero1Suffix;
  document.title = text.title;
  document.querySelector('meta[name="description"]').content = text.description;
  languageSelect.value = languagePreference;
  languageSelect.setAttribute('aria-label', text.language);
  document.querySelector('.language-control').title = text.language;
  renderAppearanceControl();
  appearanceSelect.setAttribute('aria-label', text.appearance);
  document.querySelector('nav').setAttribute('aria-label', text.navigation);
  document.querySelector('.github-link').setAttribute('aria-label', text.github);
  document.querySelector('.github-link').title = text.github;
  document.querySelector('.showcase').setAttribute('aria-label', text.screenshots);
  document.querySelector('.tabs').setAttribute('aria-label', text.views);
  for (const image of screenshots) {
    image.alt = text[image.dataset.screen + 'Alt'];
  }
  storage.write('koto-site-language', languagePreference);
  renderScreenshots();
  renderAssets();
  renderDownload(text);
  document.querySelector('.cli-demo').setAttribute('aria-label', text.cliDemo);
  dialogue.play([text.cliRequest, text.cliResponse, text.cliInstruction, text.cliCommand], locale);
  typewriter.play(text.hero2, locale);
}
languageSelect.addEventListener('change', () => setLanguage(languageSelect.value));
window.addEventListener('languagechange', () => { if (languagePreference === 'system') setLanguage('system'); });
setLanguage(languagePreference);


const appearance = watchAppearance(matchMedia('(prefers-color-scheme: dark)'), appearancePreference, theme => {
  document.documentElement.dataset.theme = theme;
  activeTheme = theme;
  renderScreenshots();
});
appearanceSelect.value = appearancePreference;
appearanceSelect.addEventListener('change', () => {
  appearancePreference = appearanceSelect.value;
  storage.write('koto-site-appearance', appearancePreference);
  appearance.set(appearancePreference);
  renderAppearanceControl();
});

function selectTab(tab, focus = false) {
  for (const current of tabs) {
    const selected = current === tab;
    current.setAttribute('aria-selected', String(selected));
    current.tabIndex = selected ? 0 : -1;
    document.getElementById(current.getAttribute('aria-controls')).hidden = !selected;
    const description = document.getElementById(current.getAttribute('aria-describedby'));
    description.classList.toggle('is-active', selected);
    description.setAttribute('aria-hidden', String(!selected));
  }
  if (focus) tab.focus();
}
for (const [index, tab] of tabs.entries()) {
  tab.addEventListener('click', () => selectTab(tab));
  tab.addEventListener('keydown', event => {
    if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) {
      event.preventDefault();
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
      selectTab(tabs[next], true);
    }
  });
}

for (const select of [languageSelect, appearanceSelect, releaseSelect]) {
  select.addEventListener("pointerdown", () => { select.dataset.pointerFocus = "true"; });
  select.addEventListener("keydown", () => { delete select.dataset.pointerFocus; });
  select.addEventListener("blur", () => { delete select.dataset.pointerFocus; });
}
document.addEventListener("keydown", event => {
  if (event.key === "Tab") for (const select of [languageSelect, appearanceSelect, releaseSelect]) delete select.dataset.pointerFocus;
});

// Detection stays in memory and never replaces a user's explicit choice.
detectSystem(navigator).then(result => {
  detectedSystem = result;
  if (!releaseManuallySelected) renderAssets();
  renderDownload();
});
