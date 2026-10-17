// Enable only after these assets are available on the GitHub release.
export const publication = Object.freeze({
  published: false,
  version: '0.1.0-beta.1',
  repository: 'https://github.com/Seanium/Koto'
});

export const releaseAssets = Object.freeze([
  { id: 'macos-arm64', label: 'macOS · Apple silicon · DMG', suffix: 'macos-arm64.dmg' },
  { id: 'macos-amd64', label: 'macOS · Intel · DMG', suffix: 'macos-amd64.dmg' },
  { id: 'linux-arm64-deb', label: 'Linux · ARM64 · DEB', suffix: 'linux-arm64.deb' },
  { id: 'linux-amd64-deb', label: 'Linux · x86_64 · DEB', suffix: 'linux-amd64.deb' },
  { id: 'linux-arm64-tar', label: 'Linux · ARM64 · tar.gz', suffix: 'linux-arm64.tar.gz' },
  { id: 'linux-amd64-tar', label: 'Linux · x86_64 · tar.gz', suffix: 'linux-amd64.tar.gz' }
]);

export function downloadURL(assetId, config = publication) {
  const asset = releaseAssets.find(value => value.id === assetId);
  if (!config.published || !asset) return null;
  return `${config.repository}/releases/download/v${config.version}/koto-${config.version}-${asset.suffix}`;
}
