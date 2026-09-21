# Ariadne

Ariadne is a persistent terminal multiplexer for macOS, Linux, and Windows. It
runs as one executable, keeps terminal sessions in a background daemon, and
provides a full-screen TUI with workspaces, windows, split panes, scrollback,
clipboard integration, ToolPanes, and trusted local plugins.

日本語概要: Ariadneは、macOS・Linux・Windowsで動作する常駐型ターミナル
マルチプレクサです。単一実行ファイルでdaemonとTUIを提供し、Workspace、
Window、Pane、履歴、clipboard、Pluginを共通Core上で管理します。GUIは
v1.0.0には含まれません。

## Install

Download the archive for your OS and architecture from GitHub Releases, extract
it, and place `ariadne` (`ariadne.exe` on Windows) on `PATH`.

Official archives are provided for:

- macOS 13 or later: amd64, arm64
- Linux with glibc 2.35 or later: amd64, arm64
- Windows 10 version 1809 or later: amd64, arm64

The v1.0 binaries are not Apple-notarized or OS code-signed. Every archive has
a SHA-256 checksum and a GitHub build-provenance attestation:

```sh
sha256sum -c SHA256SUMS
gh attestation verify ariadne_v1.0.0_linux_amd64.tar.gz -R aruzen/project-ariadne
```

## Quick start

```sh
ariadne init       # Write a commented config template; never overwrites one.
ariadne tui        # Start or connect to the daemon and open the TUI.
ariadne --help
ariadne --version
```

The default prefix is `Ctrl-a`:

| Keys | Action |
| --- | --- |
| `Ctrl-a h/j/k/l` | Focus left/down/up/right |
| `Ctrl-a Ctrl-h/j/k/l` | Resize the focused split |
| `Ctrl-a %` / `Ctrl-a "` | Split left-right / top-bottom |
| `Ctrl-a z` | Toggle zoom |
| `Ctrl-a [` / `Ctrl-a ]` | Copy mode / paste |
| `Ctrl-a :` | Open the command prompt |
| `Ctrl-a ?` | Show TUI help |
| `Ctrl-a d` | Detach |

Pane processes remain managed by the daemon after a frontend detaches. A process
that exits normally leaves its pane available for `restart`, `run`, or deletion.
Use `ariadne help <command>` for CLI details.

## Configuration and data

`ariadne init` writes `config.toml` to the first applicable location:

1. `$ARIADNE_CONFIG_PATH/config.toml`
2. `$XDG_CONFIG_HOME/ariadne/config.toml`
3. `~/.config/ariadne/config.toml`

The format is strict TOML: unknown fields and invalid values are rejected. The
template documents shells, editors, keybindings, themes, status widgets,
clipboard policy, transport limits, and plugin limits. Clipboard reads and
writes default to `ask` and accept text only.

Persistent layout and launch metadata are stored separately from configuration.
Clipboard contents, PTY history, environment variables, and frontend-only state
are not written to the state file.

## Plugins

Ariadne supports trusted local process plugins and native C/C++ shared-library
plugins. Native libraries are loaded into a disposable helper process rather
than the daemon. API capabilities restrict Ariadne operations, but they are not
an OS sandbox: install only code you trust.

```sh
ariadne plugin install /absolute/path/to/package
ariadne plugin grant PLUGIN_ID core.read workspace:1
ariadne plugin enable PLUGIN_ID
ariadne plugin list
```

The external Plugin API v1 is documented in
[`docs/plugin-protocol-v1.md`](docs/plugin-protocol-v1.md). Examples are under
`examples/plugins/`.

## Build from source

Requirements are Go 1.27 and Zig 0.16.0. The build tool fetches the pinned
Ghostty revision and statically links libghostty-vt into Ariadne.

```sh
make build
./build/ariadne --version
```

Run the full validation suite with:

```sh
go test -race -shuffle=on ./...
go vet ./...
```

## Compatibility

- Plugin API v1, documented CLI commands, and configuration keys remain
  compatible throughout Ariadne 1.x.
- Existing supported state-file versions are migrated forward. Downgrading a
  state file is not supported.
- A daemon and its frontends should use the same Ariadne release.
- GUI support is planned after the TUI v1 series and will use the same daemon
  protocol and Core model.

## License

Ariadne is available under the MIT License. See [`LICENSE`](LICENSE) and
[`THIRD_PARTY_NOTICES`](THIRD_PARTY_NOTICES).
