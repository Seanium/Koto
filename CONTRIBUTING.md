# Contributing

[English](CONTRIBUTING.md) · [简体中文](docs/zh/contributing.md) · [繁體中文](docs/zh-TW/contributing.md) · [日本語](docs/ja/contributing.md) · [Deutsch](docs/de/contributing.md)

Describe the problem, the change, and what you tested. For a connector change, include the native format and Agent version.

Use the [development guide](docs/development.md) to build and run the tests. Use synthetic fixtures and new disposable sessions; keep personal conversations, credentials, databases, and test logs out of the repository.

Check the behavior affected by your change:

- **UI:** light/dark appearance, narrow windows, keyboard controls, and long translations.
- **Copying:** selected content, same-Agent copies, repeated copies, attachments, and path conflicts. The source must stay unchanged.
- **Native code:** build and run on the affected platform. Report compilation, installation, and GUI results separately.

Contributions use the project's MIT License. Preserve third-party notices when changing dependencies or assets.
