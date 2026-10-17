// Set published only after every listed asset is available on the release.
export const publication = Object.freeze({
  published: true,
  version: '0.1.0-beta.2',
  repository: 'https://github.com/Seanium/koto'
});

export const releaseAssets = Object.freeze([
  { id: 'macos-arm64', product: 'app', label: 'macOS · Apple silicon · DMG', file: 'koto-darwin-arm64.dmg' },
  { id: 'macos-amd64', product: 'app', label: 'macOS · Intel · DMG', file: 'koto-darwin-amd64.dmg' },
  { id: 'windows-arm64', product: 'app', label: 'Windows · ARM64 · EXE', file: 'koto-windows-arm64.exe' },
  { id: 'windows-amd64', product: 'app', label: 'Windows · x64 · EXE', file: 'koto-windows-amd64.exe' },
  { id: 'linux-arm64', product: 'app', label: 'Linux · ARM64', file: 'koto-linux-arm64' },
  { id: 'linux-amd64', product: 'app', label: 'Linux · x86_64', file: 'koto-linux-amd64' },
  { id: 'linux-arm64-deb', product: 'app', label: 'Linux · ARM64 · DEB', file: 'koto-linux-arm64.deb' },
  { id: 'linux-amd64-deb', product: 'app', label: 'Linux · x86_64 · DEB', file: 'koto-linux-amd64.deb' },
  { id: 'cli-macos-arm64', product: 'cli', label: 'macOS · Apple silicon', file: 'koto-cli-darwin-arm64' },
  { id: 'cli-macos-amd64', product: 'cli', label: 'macOS · Intel', file: 'koto-cli-darwin-amd64' },
  { id: 'cli-windows-arm64', product: 'cli', label: 'Windows · ARM64 · EXE', file: 'koto-cli-windows-arm64.exe' },
  { id: 'cli-windows-amd64', product: 'cli', label: 'Windows · x64 · EXE', file: 'koto-cli-windows-amd64.exe' },
  { id: 'cli-linux-arm64', product: 'cli', label: 'Linux · ARM64', file: 'koto-cli-linux-arm64' },
  { id: 'cli-linux-amd64', product: 'cli', label: 'Linux · x86_64', file: 'koto-cli-linux-amd64' }
]);

export function downloadURL(assetId, config = publication) {
  const asset = releaseAssets.find(value => value.id === assetId);
  if (!config.published || !asset) return null;
  return `${config.repository}/releases/download/v${config.version}/${asset.file}`;
}

export function matchingAsset(system, product = 'app') {
  if (!system?.arch) return null;
  const id = `${product === 'cli' ? 'cli-' : ''}${system.platform}-${system.arch}`;
  return releaseAssets.some(asset => asset.id === id && asset.product === product) ? id : null;
}
