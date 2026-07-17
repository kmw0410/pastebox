# Pastebox CLI Packages

`pastebox-cli` installs the `pb` terminal client for uploading text to and retrieving raw text from a Pastebox server.

## Supported packages

GitHub Releases provide packages for these Linux architectures:

| Distribution | amd64 | arm64 |
|---|---|---|
| Debian / Ubuntu | `amd64.deb` | `arm64.deb` |
| Arch Linux family | `x86_64.pkg.tar.zst` | `aarch64.pkg.tar.zst` |

## Install

Download the package for your system from the matching GitHub Release.

Debian or Ubuntu:

```bash
sudo apt install ./pastebox-cli_VERSION-1_amd64.deb
```

Arch Linux, Manjaro, or EndeavourOS:

```bash
sudo pacman -U ./pastebox-cli-VERSION-1-x86_64.pkg.tar.zst
```

Use the `arm64` Debian package or `aarch64` Arch package on 64-bit ARM systems.

## Configure

The CLI reads only this per-user configuration file:

```text
~/.config/pastebox/config.json
```

Run `pb` without input once to create it:

```bash
pb
```

```text
created config: /home/user/.config/pastebox/config.json
Edit server_url in this file before using pb.
```

The generated file has user-only `0600` permissions and an empty `server_url`. Edit it and set your Pastebox server before uploading:

```json
{
  "server_url": "https://paste.example.com"
}
```

A server installed below a URL path is also supported, for example `https://example.com/pastebox`. Running `pb` again never overwrites an existing config file.

Validate the file before uploading:

```bash
pb config validate
```

The validator reports the config path and the specific malformed field or URL. JSON line and column numbers are included when available.

## Upload

Upload a file while preserving its original filename:

```bash
pb server.log
```

Upload piped text:

```bash
journalctl -u nginx | pb
printf 'hello\n' | pb
```

Pastebox uses its normal temporary retention policy by default. Other policies and upload options are available:

```bash
pb --permanent config.yaml
pb --once message.txt
pb --expires 12h build.log
pb --password secret.txt
pb --code deploy-log --label "Production deployment" server.log
```

`--permanent`, `--once`, and `--expires` cannot be combined.

The normal successful output contains the public URL and, when returned by the server, its expiration, generated password, private manage URL, and delete URL:

```text
url: https://paste.example.com/AbC123
expires: 2026-08-16T10:00:00+09:00
password: GENERATED_PASSWORD
manage: https://paste.example.com/AbC123?manage=MANAGE_TOKEN
delete: https://paste.example.com/AbC123?delete=DELETE_TOKEN
```

For scripts, print only the public URL or request JSON output:

```bash
URL="$(pb --quiet server.log)"
pb --json server.log
```

`--quiet` and `--json` cannot be combined.

## Retrieve raw text

Retrieve a paste by code or by a public URL:

```bash
pb get AbC123
pb get https://paste.example.com/AbC123
pb get AbC123 > restored.log
```

For a protected paste, provide its password through the request header with:

```bash
pb get --password 'PASTE_PASSWORD' AbC123
```

The CLI accepts full paste URLs only when they belong to the server configured in `config.json`. This prevents accidentally sending a paste password to another host.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | Network, server, or output failure |
| `2` | Invalid arguments, input, or configuration |

## Verify downloads

Each release includes `checksums.txt`:

```bash
sha256sum --check checksums.txt
```

The command checks every CLI package listed in the file, so all listed package files must be present in the same directory.

## Remove

Debian or Ubuntu:

```bash
sudo apt remove pastebox-cli
```

Arch Linux family:

```bash
sudo pacman -R pastebox-cli
```

Package removal does not delete `~/.config/pastebox/config.json`.
