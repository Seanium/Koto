export async function detectSystem(browser) {
  const userAgent = browser.userAgent || '';
  const platform = browser.userAgentData?.platform || browser.platform || '';
  const mobile = browser.userAgentData?.mobile || /Android|iPhone|iPad|Mobile/i.test(userAgent)
    || (/Mac/i.test(platform) && browser.maxTouchPoints > 1);
  const system = mobile ? 'mobile' : /Win/i.test(platform + userAgent) ? 'windows'
    : /Mac/i.test(platform + userAgent) ? 'macos' : /Linux/i.test(platform + userAgent) ? 'linux' : 'unknown';
  const result = { platform: system, arch: null, assetId: null };
  if (!['macos', 'linux', 'windows'].includes(system)) return result;

  // Reduced user agents can report Intel/x86_64 on ARM devices. Never infer CPU from them.
  try {
    const hints = await browser.userAgentData?.getHighEntropyValues(['architecture', 'bitness']);
    const architecture = (hints?.architecture || '').toLowerCase();
    if (['arm64', 'aarch64'].includes(architecture) || architecture === 'arm' && hints.bitness === '64') result.arch = 'arm64';
    if (['x86_64', 'amd64'].includes(architecture) || architecture === 'x86' && hints.bitness === '64') result.arch = 'amd64';
  } catch { /* The chooser remains available when the browser withholds CPU hints. */ }
  if (result.arch) result.assetId = `${system}-${result.arch}`;
  return result;
}
