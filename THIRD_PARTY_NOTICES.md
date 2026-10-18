# Third-party notices

koto's own source is under the [MIT License](LICENSE). Third-party components retain their own licenses. Dependency caches and downloaded toolchains are not distributed as source.

## Included assets and generated files

- Except for ZCode and WorkBuddy, agent-identification SVGs in `app/frontend/src/assets/harness/` and their copies in `site/assets/` come from [LobeHub Icons](https://github.com/lobehub/lobe-icons), MIT, © 2023 LobeHub. Cursor is copied directly; the others come via [magpie](https://github.com/yetone/magpie), MIT, © 2026 yetone. The complete notices are in `licenses/assets/`. koto does not depend on magpie.
- The ZCode identification mark is from [zai-org/ZCode](https://github.com/zai-org/ZCode/blob/aac4755666d09fdcd70272fcf063c077a639015f/packages/ui/src/components/ui/ZCodeAboutLogo.tsx), Apache-2.0. Its path geometry is unchanged; the React wrapper was replaced with an SVG file. The license is preserved in [licenses/assets/zcode-LICENSE](licenses/assets/zcode-LICENSE).
- The WorkBuddy mark is unchanged artwork from Tencent's signed WorkBuddy and [WorkBuddy AI](https://www.workbuddy.ai/) App bundles, used to identify those Apps. It is not covered by koto's MIT license; provenance and checksum are in [app/ASSETS.md](app/ASSETS.md).
- The fixed Apple speech-balloon App, tray, and website icons and 🐙 / 🐳 installer artwork were rendered on macOS using Apple Color Emoji. No Apple font file is bundled. These Apple graphics are not covered by koto's MIT license. Rendering sources and usage are described in [app/ASSETS.md](app/ASSETS.md).
- Wails-generated build scaffolding and bindings are covered by the Wails MIT notice in `licenses/go/github.com_wailsapp_wails_v3@v3.0.0-beta.24/LICENSE`.
- Site screenshots show synthetic conversations. The website uses the same fixed Apple speech-balloon artwork as the App.
- The website's GitHub mark is from [Primer Octicons](https://github.com/primer/octicons), MIT, © 2026 GitHub, Inc. The notice is preserved in [licenses/assets/octicons-LICENSE](licenses/assets/octicons-LICENSE).

Names and logos identify their respective agents. They do not imply sponsorship or endorsement, and trademark rights remain with their owners.

## Dependency license copies

`licenses/go/` preserves license and notice files for modules pinned in `app/go.mod`, including relevant bundled third-party notices. `licenses/npm/` preserves notices for frontend packages in the lockfile that are installed in the reference build environment. `licenses/INDEX.json` records the package versions and license file hashes.

The frontend runtime uses React, React DOM, zustand, scheduler, and the Wails runtime. Build tooling includes Vite, TypeScript, and their dependencies. The Go runtime uses Wails, modernc SQLite, klauspost/compress, and the modules declared in `app/go.mod`. Exact versions are in the lockfiles and license index.

When updating dependencies, refresh the corresponding notices from those exact package versions. Binary distributors should verify the notices for their actual build, including platform-specific or newly added dependencies.
