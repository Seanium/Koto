import { content, commands } from './docs-content.mjs?v=0.1.0-download-20261019';
import { locales, resolveLocale, watchAppearance } from './preferences.mjs?v=0.1.0-download-20261019';
import { translations } from './translations.mjs?v=0.1.0-positioning-20261019';

const readPreference = key => { try { return localStorage.getItem(key); } catch { return null; } };
const savePreference = (key, value) => { try { localStorage.setItem(key, value); } catch {} };
const queryLanguage = new URL(location.href).searchParams.get('lang');
let preference = locales.includes(queryLanguage) ? queryLanguage : readPreference('koto-site-language') || 'system';
if (locales.includes(queryLanguage)) savePreference('koto-site-language', queryLanguage);
if (!locales.includes(preference)) preference = 'system';
let appearance = readPreference('koto-site-appearance') || 'system';
if (!['light', 'dark'].includes(appearance)) appearance = 'system';
let locale;
const languageSelect = document.querySelector('#language-select');
const appearanceSelect = document.querySelector('#appearance-select');

function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  if (className) node.className = className;
  return node;
}

function paragraph(text, className) {
  const node = element('p', undefined, className);
  text.split(/(`[^`]+`)/g).forEach(part => node.append(part.startsWith('`') ? element('code', part.slice(1, -1)) : document.createTextNode(part)));
  return node;
}

function codeBlock(label, command) {
  const box = element('div', undefined, 'docs-code');
  const head = element('div', undefined, 'docs-code-head');
  head.append(element('span', label));
  const button = element('button');
  button.type = 'button';
  button.title = content[locale].copyCommand;
  button.setAttribute('aria-label', `${content[locale].copyCommand}: ${label}`);
  button.innerHTML = '<svg aria-hidden="true" viewBox="0 0 24 24"><rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>';
  button.addEventListener('click', async () => {
    const feedback = document.querySelector('#docs-feedback');
    try {
      await navigator.clipboard.writeText(command);
      feedback.textContent = content[locale].copied;
      button.title = content[locale].copied;
      button.setAttribute('aria-label', content[locale].copied);
      button.innerHTML = '<svg aria-hidden="true" viewBox="0 0 24 24"><path d="m5 12 4 4L19 6"/></svg>';
    }
    catch { feedback.textContent = content[locale].copyFailed; }
  });
  head.append(button);
  const pre = element('pre');
  pre.tabIndex = 0;
  pre.setAttribute('aria-label', label);
  pre.append(element('code', command));
  box.append(head, pre);
  return box;
}

function section(id, title, children) {
  const node = element('section', undefined, 'docs-section');
  node.id = id;
  node.setAttribute('aria-labelledby', `${id}-title`);
  const heading = element('h3', title);
  heading.id = `${id}-title`;
  node.append(heading, ...children);
  return node;
}

function chapter(id, title, intro, children) {
  const node = element('section', undefined, 'docs-chapter');
  node.id = id;
  node.setAttribute('aria-labelledby', `${id}-title`);
  const heading = element('h2', title);
  heading.id = `${id}-title`;
  node.append(heading, paragraph(intro), ...children);
  return node;
}

function referenceLink(label, file) {
  const link = element('a', label);
  const folder = locale === 'en' ? '' : `${locale}/`;
  link.href = `https://github.com/Seanium/koto/blob/main/docs/${folder}${file}.md`;
  return link;
}

function render() {
  locale = resolveLocale(preference, navigator.language);
  const t = content[locale];
  const shared = translations[locale];
  document.documentElement.lang = locale === 'zh' ? 'zh-CN' : locale;
  document.title = `${t.title} — koto App & CLI`;
  document.querySelector('meta[name=description]').content = t.description;
  document.querySelectorAll('[data-doc]').forEach(node => { node.textContent = t[node.dataset.doc]; });
  document.querySelectorAll('[data-i18n]').forEach(node => { node.textContent = shared[node.dataset.i18n]; });
  languageSelect.value = preference;
  languageSelect.setAttribute('aria-label', shared.language);
  languageSelect.closest('label').title = shared.language;
  appearanceSelect.setAttribute('aria-label', shared.appearance);
  appearanceSelect.closest('label').title = shared.appearance;
  document.querySelector('.header nav').setAttribute('aria-label', shared.navigation);
  document.querySelector('.github-link').setAttribute('aria-label', shared.github);
  document.querySelector('.github-link').title = shared.github;
  const nav = document.querySelector('#docs-nav');
  nav.setAttribute('aria-label', t.navigation);
  nav.replaceChildren();
  [['app', 'App', true], ['app-browse', t.browseTitle], ['app-copy', t.copyTitle], ['app-compare', t.compareTitle], ['app-update', t.updateTitle], ['cli', 'CLI', true], ['cli-install', t.installTitle], ['cli-conversation', t.conversationTitle], ['cli-commands', t.commandsTitle], ['cli-update', t.updateTitle]].forEach(([id, title, isChapter]) => {
    const link = element('a', title, isChapter ? 'docs-nav-chapter' : 'docs-nav-section');
    link.href = `#${id}`;
    nav.append(link);
  });
  const steps = element('ol');
  t.copySteps.forEach(text => steps.append(element('li', text)));
  const app = chapter('app', 'koto App', t.appIntro, [
    section('app-browse', t.browseTitle, t.browse.map(text => paragraph(text))),
    section('app-copy', t.copyTitle, [steps, paragraph(t.copyNote, 'docs-note')]),
    section('app-compare', t.compareTitle, [paragraph(t.compare)]),
    section('app-update', t.updateTitle, [paragraph(t.appUpdate)]),
  ]);
  const appLinks = element('div', undefined, 'docs-links');
  appLinks.append(referenceLink(t.compatibility, 'compatibility'));
  app.append(appLinks);
  const cli = chapter('cli', 'koto CLI', t.cliIntro, [
    section('cli-install', t.installTitle, [codeBlock(t.unixLabel, commands.unix), codeBlock(t.windowsLabel, commands.windows), paragraph(t.installNote)]),
    section('cli-conversation', t.conversationTitle, [element('blockquote', t.prompt, 'docs-prompt'), paragraph(t.destinationNote), paragraph(t.conversation), codeBlock(t.currentLabel, commands.current), paragraph(t.currentNote), paragraph(t.receipt)]),
    section('cli-commands', t.commandsTitle, [codeBlock(t.browseLabel, commands.browse), codeBlock(t.copyLabel, commands.copy), codeBlock(t.targetsLabel, commands.targets), codeBlock(t.profilesLabel, commands.profiles), codeBlock(t.compareLabel, commands.compare), paragraph(t.commandsNote, 'docs-note')]),
    section('cli-update', t.updateTitle, [paragraph(t.update), codeBlock('koto update', commands.update), paragraph(t.updateNote)]),
  ]);
  const cliLinks = element('div', undefined, 'docs-links');
  cliLinks.append(referenceLink(t.fullReference, 'usage'), referenceLink(t.compatibility, 'compatibility'));
  cli.append(cliLinks);
  document.querySelector('#docs-content').replaceChildren(app, cli);
}

const appearanceWatcher = watchAppearance(matchMedia('(prefers-color-scheme: dark)'), appearance, value => { document.documentElement.dataset.theme = value; });
function syncAppearance() {
  appearanceSelect.value = appearance;
  document.querySelectorAll('[data-appearance]').forEach(icon => { icon.hidden = icon.dataset.appearance !== appearance; });
  appearanceWatcher.set(appearance);
}
languageSelect.addEventListener('change', () => {
  preference = languageSelect.value;
  savePreference('koto-site-language', preference);
  const url = new URL(location.href);
  url.searchParams.delete('lang');
  history.replaceState(null, '', url);
  render();
});
appearanceSelect.addEventListener('change', () => {
  appearance = appearanceSelect.value;
  savePreference('koto-site-appearance', appearance);
  syncAppearance();
});
window.addEventListener('languagechange', () => { if (preference === 'system') render(); });
window.addEventListener('storage', event => {
  if (event.key === 'koto-site-language') { preference = locales.includes(event.newValue) ? event.newValue : 'system'; render(); }
  if (event.key === 'koto-site-appearance') { appearance = ['light', 'dark'].includes(event.newValue) ? event.newValue : 'system'; syncAppearance(); }
});
for (const select of [languageSelect, appearanceSelect]) {
  select.addEventListener('pointerdown', () => { select.dataset.pointerFocus = 'true'; });
  select.addEventListener('keydown', () => { delete select.dataset.pointerFocus; });
  select.addEventListener('blur', () => { delete select.dataset.pointerFocus; });
}
document.addEventListener('keydown', event => {
  if (event.key === 'Tab') for (const select of [languageSelect, appearanceSelect]) delete select.dataset.pointerFocus;
});
render();
syncAppearance();
if (location.hash) requestAnimationFrame(() => document.getElementById(location.hash.slice(1))?.scrollIntoView());
