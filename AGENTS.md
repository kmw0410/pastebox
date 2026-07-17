# AGENTS.md

## 1. Project Overview
Pastebox is a lightweight self-hosted Go service for curl-friendly text sharing.

Core storage model:
- Paste content is stored as local files.
- Metadata is stored as JSON sidecar files.
- SQLite is used only for admin accounts, admin sessions, service settings, and upload-disable state.
- Do not move paste content into SQLite unless explicitly requested.

The service targets simple Docker operation, minimal dependencies, and a dark UI optimized for text-based workflows.

## 2. Directory Structure
```text
pastebox/
├── Dockerfile
├── docker-compose.yml
├── docker-compose-dockerhub.yml
├── docker-entrypoint.sh
├── go.mod
├── go.sum
├── README.md
├── README_ko.md
├── AGENTS.md
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── metadata.go
│   └── store.go
├── locales/
│   ├── en.json
│   └── ko.json
└── templates/
    ├── 404.html
    ├── admin_form.html
    ├── admin_list.html
    ├── admin_reset.html
    ├── clone.html
    ├── index.html
    ├── manage.html
    ├── password.html
    ├── paste.html
    └── (optional design prototypes)
```

## 3. Architecture
- HTTP server: `cmd/server/main.go`
  - Routing, upload handling, paste view/raw view, password checks, clone flow, manage flow, admin auth/reset/controls, template rendering, i18n loading.
- Storage layer: `internal/store.go`, `internal/metadata.go`
  - File persistence, metadata persistence, admin/session/settings persistence in SQLite, cleanup, locking.

Typical runtime layout:
```text
/paste-data/<code>
/paste-data/<code>.json
/paste-data/pastebox.db
```
Host mapping is typically `./data:/paste-data`.

## 4. Critical Rules
- E2EE was intentionally removed. Do not reintroduce E2EE (flags, key files, encrypted metadata, encrypt/decrypt helpers) unless explicitly requested.
- Generated paste passwords must never include `=`.
  - Keep special set compatible with URL query use.
- HTML must remain in external template files under `templates/`; do not inline large templates in Go code.
- Preserve fine-grained per-paste locking in the store layer; do not replace with a single global lock.
- When asked which files were modified, respond only with files changed in the most recent edit scope.
- When committing, exclude `WORK.md` and `AGENTS.md` unless the user explicitly asks to include them.
- When committing, add a `Co-authored-by: Codex <codex@openai.com>` trailer unless the user explicitly asks not to.
- If a change scope would be split into 2 or more commits, ask before pushing; otherwise commit only unless the user explicitly says to push.
- When modifying markdown files, include the filename in the commit subject or body so the changed docs are obvious.
- Keep commit messages themselves in English and follow the existing short conventional style.
- Before starting work, always consult `WORK.md` alongside `AGENTS.md` so recent task history is part of the working context.
- After any commit+push sequence, record in `WORK.md` what work was done and what mostly changed, using the existing date-based log format, and include the commit ID plus commit message when available.
- If a follow-up fix is needed because of the agent's own mistake, record that in `WORK.md` as well, including what was wrong and how it was corrected.
- In `WORK.md`, every work-entry line under a date heading must start with `- ` so dated blocks are easy to scan.
- When a problem, regression, or unexpected behavior is reported or discovered, inspect the relevant git history before fixing it. Use commands such as `git log --follow -p -- <file>` and `git blame <file>` to identify when the affected code changed and what part changed.
- When fixing code after an error occurs, record the problematic code area, the root cause, and the fix in `AGENTS.md`, including a small relevant code snippet when useful.
- Before making a similar future change, review those recorded error-fix notes to avoid repeating the same mistake.

## 5. Upload Behavior
Supported upload methods:
```bash
echo "hello" | curl -X POST --data-binary @- http://localhost:8080/
curl -F "file=@test.txt" http://localhost:8080/
```

Text-only policy:
- Allow text-oriented types/extensions (logs, source files, markdown, config formats, etc.).
- Block binary/media/archive/image uploads.

Detection and memory rule:
- Use leading-sample text detection.
- Do not load full file contents into memory for large uploads.
- Avoid full-file `io.ReadAll(reader)` upload paths.

Limits and metadata:
- Upload size limit is 1 GiB.
- Exceeding limit must return `413 Payload Too Large`.
- Multipart uploads must store original filename in metadata.
- Raw-body uploads should keep filename empty.
- Admin list and paste view should display filename when present (fallback `-` in admin list).

## 6. Upload Options and Responses
Policies:
- Temporary (default): expires by configured TTL.
- Permanent: `data-policy: permanent` (no automatic expiration).
- One-time: `data-policy: once` (deleted after first successful view).
- Password-protected: `usepassword: true` with an 8-character generated password.

Password access methods:
- Query: `?password=<password>`
- Header: `paste-password: <password>`

Custom code:
- Supplied through `code` header.
- Max length: 10 characters.
- Allowed chars only: `A-Z`, `a-z`, `0-9`, `_`, `-`.
- Duplicate code must return `409 Conflict`.

Successful upload response should include:
- `url`
- `manage`
- `expires` (when applicable; omitted for permanent)
- `delete`
- `password` (when applicable)

Response formats:
- Upload success and upload error responses may return JSON when `?format=json` is present.
- Raw paste responses use `?raw=1` on the paste URL.

Delete token handling:
- Never log delete tokens or manage tokens.
- Never expose delete tokens in admin UI.

## 7. Paste Viewing
Paste view (`templates/paste.html`) must include:
- Paste code
- Original filename (when present)
- Language badge
- Copy button
- Clone button
- Line number toggle
- Raw button
- Syntax highlighting
- Horizontal scrolling for long lines

Behavior rules:
- Copy must copy only paste content, never line numbers.
- Raw mode (`?raw=1`) must return original text content, not the HTML viewer.
- Line numbers must be disabled by default.
- When enabled, line numbers must always stay on the left.
- Do not reintroduce right-side line-number layout regressions.
- `.log` files should display language as `logs` (not `plaintext`).
- Markdown must be shown as highlighted code, not rendered preview.

## 8. Clone and Management Behavior
- Clone must be triggered from the paste view page UX.
- Do not require users to manually use `/clone` or `?clone=1` as a primary UX.
- Clone must preserve original filename.
- Clone must be blocked when uploads are disabled.
- Clone result page (`templates/clone.html`) should show URL, expiration (if any), generated password (if any), manage URL, and delete URL.
- Every successful upload and clone also produces a private manage URL using `?manage=<token>`.
- Manage page (`templates/manage.html`) should allow viewing paste metadata, copying the public/manage URLs, enabling or disabling password protection, changing the retention policy, and deleting the paste.
- Disabling password protection from the manage page requires the current password.
- When password protection is enabled from the manage page, the generated password must be shown once and then only stored hashed.

## 9. Admin Behavior
Admin entry point: `/admin`.

Admin account rules:
- If no admin exists, the first created account becomes the only administrator account.
- Additional admin registration must remain disabled afterward.
- Admin passwords must never be stored in plain text.
- Admin sessions are stored in SQLite, issued as HttpOnly cookies, and expire after 24 hours.
- Setup, login, and reset forms are CSRF-protected.
- Admin login and reset attempts are rate limited.

Upload disable behavior:
- State must be stored in SQLite.
- When uploads are disabled:
  - New uploads are rejected.
  - Clone is rejected.
  - Existing paste view and deletion still work.
  - Admin page still works.

Delete controls:
- Individual delete must not show browser confirm popup.
- Bulk delete must keep a confirmation popup and clear irreversible warning.
- Delete result messages must not use query params like `/admin?deleted=...`.
- Use internal flash messages (for example short-lived HttpOnly cookie) for delete results.
- Admin delete should support both a single paste and a multi-select bulk delete action.

Required admin copy:
- Setup page: `The first account becomes the only administrator account.`
- Login page: `Sign in with the administrator account to manage stored pastes.`
- Keep setup and login copy distinct.

Admin list timestamps should be converted to local time before rendering.

## 10. Language and Localization
- UI language is selected with `LANGUAGE` env var.
- Supported values: `en`, `ko`.
- Missing/empty/invalid values must fallback to `en`.

Locale files:
- `locales/en.json` is the default English source.
- `locales/ko.json` may provide Korean translations.
- Missing or empty Korean values must fallback to English.

Template translation:
- Templates should use `{{ t "key" }}`.
- Go server must register `t` when parsing templates.

Korean wording rule:
- Do not translate “paste” as `붙여넣기`.
- Prefer domain wording like `Paste`, `Paste 링크`, `저장된 Paste`, `Paste 열기`, `Paste 삭제`.

## 11. Docker and Runtime Configuration
Dockerfile/runtime expectations:
- Dockerfile should copy `templates/`, `locales/`, and `docker-entrypoint.sh`.
- Image must include `locales/`.
- `docker-compose.yml` is for local build usage.
- `docker-compose-dockerhub.yml` is for Docker Hub image usage.

Compose rule:
- `LANGUAGE` must be under `environment`, not at service root.

`MIRROR_URL` rules:
- Intended for Docker Hub runtime usage.
- It only changes `/etc/apk/repositories` inside the running container.
- It does not affect Docker Hub pull behavior.
- It does not affect apk commands already executed during build.
- If empty, entrypoint must not modify repositories.

Entrypoint responsibilities:
- Create `DATA_DIR`.
- Fix bind-mount permissions.
- Optionally apply `MIRROR_URL`.
- Optionally apply timezone settings.
- Drop privileges using `su-exec`.

Timezone rules:
- Default timezone should be `Asia/Seoul`.
- `TZ` should be optionally overrideable.
- On valid `TZ`, apply `/usr/share/zoneinfo/$TZ` to `/etc/localtime` and write `$TZ` to `/etc/timezone`.
- On invalid `TZ`, log warning and keep existing timezone state.

UI consistency rules:
- Expanded index page design must not regress to older simple layout.
- Index page must include:
  - text-only notice
  - text upload example
  - file upload example
  - password-protected example
  - data-policy section
  - expiration information section
  - manual deletion section
  - Home button
  - GitHub button
  - Copy curl example button
- Template styling should use the current external CSS files under `templates/css/`:
  - `templates/css/common.css` for shared layout, variables, panels, forms, and buttons.
  - `templates/css/pages.css` for index/admin/password/clone/manage/404 page layouts.
  - `templates/css/paste.css` for the paste viewer and paste toolbar.
- New UI work should follow and extend the existing dark CSS class system.
- Admin/password templates should remain visually aligned with the dark UI system.
- Password page must remain centered.
- Admin stats and upload-control cards should keep intended dark card style.

## 12. GitHub Actions
Docker publish workflow requirements:
- Push tags/images:
  - `kmw0410/pastebox:latest`
  - `kmw0410/pastebox:vYY.MM.DD`
- If same date tag exists, append incrementing suffix (`-1`, `-2`, ...).
- Multi-arch support must include:
  - `linux/amd64`
  - `linux/arm64`
- `paths-ignore` may ignore workflow files, Markdown files, and image files.

## 13. Security Rules
Never log secrets:
- Paste passwords
- Delete tokens
- Manage tokens
- Admin passwords
- Admin setup/reset tokens
- Admin session tokens

Allowed logs are operational metadata only (IDs, counters, failures, blocked reasons) without secret material.

Use security headers where appropriate:
- `X-Frame-Options: DENY`
- `Referrer-Policy: no-referrer`
- `X-Content-Type-Options: nosniff`
- `Cache-Control: no-store`

## 14. Testing Commands
```bash
# Basic upload
echo "hello" | curl -X POST --data-binary @- http://localhost:8080/

# Multipart upload
echo "hello file" > test.txt
curl -F "file=@test.txt" http://localhost:8080/

# Permanent / one-time
curl -H "data-policy: permanent" -F "file=@test.txt" http://localhost:8080/
curl -H "data-policy: once" -F "file=@test.txt" http://localhost:8080/

# Password-protected
curl -H "usepassword: true" -F "file=@secret.txt" http://localhost:8080/

# Custom code
curl -H "code: my-log" -F "file=@test.txt" http://localhost:8080/

# Raw view / password view
curl "http://localhost:8080/<code>?raw=1"
curl -H "paste-password: <password>" http://localhost:8080/<code>

# Delete
curl "http://localhost:8080/<code>?delete=<delete-token>"

# Binary block test (expect 415)
head -c 1024 /dev/urandom > binary.bin
curl -i -F "file=@binary.bin" http://localhost:8080/

# Upload limit test (expect 413)
truncate -s 1025M over_1gb.log
curl -i -F "file=@over_1gb.log" http://localhost:8080/

# Mirror check
docker exec pastebox cat /etc/apk/repositories
```

## 15. Common Mistakes to Avoid
- Putting `LANGUAGE` at service root instead of under `environment`.
- Reintroducing E2EE behavior.
- Letting generated passwords include `=`.
- Regressing index page to old simple layout.
- Breaking line-number layout so numbers appear on the right.
- Logging delete tokens or any secret fields.
- Removing `MIRROR_URL` runtime override behavior for Docker Hub compose usage.
- Replacing per-paste lock strategy with coarse global lock.
- Switching upload validation back to full-file memory reads.

## 16. Detailed Work Log
- Each dated `WORK.md` block must use at least two lines.
  - Each work-entry line under the date heading must start with `- `.
  - Line 1 should summarize what changed.
  - Line 2 should spell out the concrete code/file or behavior changes.
  - If a commit+push happened, add the commit ID and commit message on the same dated block.
  - If the same calendar date already exists, append the new notes under that date instead of creating a duplicate date heading.
- Main page design refresh:
  - Reworked index layout into expanded dark card system (`#111111`, `#E4E4E4`, accent palette), removed outer border/glow, increased width (`max-w-[98rem]`), tuned subtitle scale, and balanced top/bottom grid spacing.
- Admin/password template alignment:
  - Unified structure to dark card language, adjusted button and card behavior, ensured password page centering.
- Admin copy key cleanup:
  - Unified admin page title/description keys and removed redundant brand output in admin headers.
- Paste view and line-number alignment:
  - Added prototype work and fixed line-number baseline alignment with highlight interaction adjustments.
  - Preserved left-side line-number layout and prevented right-side regression.
- Output snapshot folder workflow:
  - Added curated design/output copies and removed non-final artifacts from the snapshot set.
- Timezone runtime improvements:
  - Added default `Asia/Seoul`, optional `TZ` override, timezone application logic in entrypoint, and warning behavior for invalid timezone values.
- Server-side local time conversion:
  - Converted admin list timestamps to local time before rendering.
- Original filename support:
  - Preserved multipart original filename in metadata and reflected it in admin and paste UI; clone preserves filename.
- Admin delete controls and flash messages:
  - Kept per-item and bulk delete behavior aligned with UX rules; delete outcomes use internal flash-message mechanics instead of query parameters.
- Localization stability:
  - Preserved JSON locale workflow (`en` default + `ko` fallback behavior) and restored localized index/admin text behavior where needed.
- Docker Hub runtime mirror override:
  - Kept runtime-only `MIRROR_URL` repository rewrite behavior in entrypoint.

## 17. Error Fix Notes
Use this section to record recurring lessons from error-driven code fixes. Each note should include:
- Problematic code area: file/function or behavior that failed.
- Cause: why the error happened.
- Fix: how it was corrected, with a small code snippet when helpful.
- Prevention: what to check before making similar changes again.

- Problematic code area: `cmd/server/main_test.go` / `TestUploadHandlerCustomDataPolicyDuration`.
  Cause: The upload response formats `expires` with `time.RFC3339`, which has second precision, while the first test bounds compared against nanosecond-precision `time.Now()` values.
  Fix: Compare against second-aligned bounds with a small upper allowance:
  ```go
  minExpires := before.Add(time.Hour).Truncate(time.Second)
  maxExpires := after.Add(time.Hour).Truncate(time.Second).Add(time.Second)
  ```
  Prevention: When testing user-visible timestamps, align assertions to the timestamp format precision before comparing parsed values.

- Problematic code area: `templates/paste.html` clone form and `cmd/server/handler_clone.go` password failure path.
  Cause: The paste view clone form posted to the same paste path without an explicit clone marker, and a protected paste clone attempt with a missing password fell through to a generic password failure instead of a password form that submits back to the clone action.
  Fix: Mark the clone form action explicitly and render the password page with a POST clone action when a browser clone request needs a password:
  ```html
  <form method="post" action="/{{ .ID }}?clone=1">
  ```
  ```go
  a.renderPasswordPage(w, r, id, "/"+id+"?clone=1", http.MethodPost, a.localizedText("paste_clone", "Clone"))
  ```
  Prevention: When adding paste-page actions that can require password authentication, make the intended action explicit in the form URL and ensure the password prompt submits back to that same action.

## 18. Commit Message Examples
Use short, conventional commit messages:
```text
feat: add language support
feat: show original filenames
docs: update AGENTS.md rules
feat: add admin delete controls
fix: keep line numbers on the left
fix: restore localized index page
perf: stream uploads after text sample validation
```

## 19. Final Guidance
Keep Pastebox:
- simple
- file-based
- curl-friendly
- Docker-friendly
- easy to self-host
- minimal in dependencies

When editing, preserve:
- file-based paste storage
- JSON sidecar metadata
- SQLite scope limited to admin/session/settings/upload-disable
- external templates under `templates/`
- text-only upload policy
- 1 GiB upload limit
- custom code validation rules
- secure secret-handling and no-secret logs
- stable left-aligned line-number layout
- clone UX from paste view page
- language fallback correctness
- Docker runtime mirror/timezone behavior

## 20. Planned CLI and Linux Packages
The terminal client should be developed as an independent Go module inside this repository rather than under `cmd/pb`.

Planned layout:
```text
pastebox/
├── cli/
│   ├── go.mod
│   ├── main.go
│   ├── config.go
│   ├── upload.go
│   ├── get.go
│   ├── output.go
│   ├── *_test.go
│   └── packaging/
│       └── nfpm.yaml
├── package.md
└── package_ko.md
```

CLI architecture and behavior:
- The installed package name should be `pastebox-cli`, and the installed command should be `pb`.
- Keep `cli/` as a separate Go module so CLI dependencies do not enter the server module.
- The CLI must communicate with Pastebox only through the public HTTP API and must not import server `internal` packages.
- Support file uploads with multipart requests so original filenames are preserved.
- Support stdin uploads with streaming raw request bodies; do not load full uploads into memory.
- Support temporary uploads by default plus permanent, once, custom expiration, generated password, custom code, and label options.
- Support normal human-readable output, public-URL-only quiet output, and JSON output.
- Support raw paste retrieval with `pb get`; send paste passwords through the `paste-password` header rather than a query string.
- Never print secrets in diagnostic logs. Normal successful output may display the password and manage/delete URLs returned for that upload because the user needs those one-time results.
- Keep CLI argument/config errors distinct from network/server errors through documented nonzero exit codes.

CLI configuration:
- Read configuration only from `~/.config/pastebox/config.json`.
- Initially support only this structure, with no config schema version:
  ```json
  {
    "server_url": "https://paste.example.com"
  }
  ```
- Do not initially add profiles, `--server`, `PASTEBOX_URL`, `XDG_CONFIG_HOME`, alternate config paths, or config set/get/show commands.
- Provide only `pb config validate` for explicit config inspection; normal CLI operations must use the same validation path automatically.
- Validation errors must identify the config path and the precise problem. Include the JSON line and column when available, name unknown or mistyped fields, and explain missing fields, invalid value types, empty values, unsupported URL schemes, missing hosts, query/fragment usage, and embedded URL credentials.
- Allow only `http://` and `https://` server URLs. Allow deployments below a URL path, normalize trailing slashes internally, and reject query strings, fragments, and embedded user credentials.
- If the config file is missing, report its exact expected path and show a minimal valid example.
- Package installation must not create a config file inside a user's home directory.

Testing and packaging:
- Run server tests and CLI-module tests separately because root `go test ./...` does not traverse a nested Go module.
- Test file/stdin streaming, headers and policies, output modes, raw/password retrieval, URL joining, config diagnostics, HTTP error mapping, exit codes, and secret-safe failures with local HTTP test servers.
- Build static CLI binaries with `CGO_ENABLED=0` for `linux/amd64` and `linux/arm64`.
- Use nFPM from `cli/packaging/nfpm.yaml` to create Debian `.deb` packages and Arch Linux `.pkg.tar.zst` packages.
- Map `amd64` to Debian `amd64` and Arch `x86_64`; map `arm64` to Debian `arm64` and Arch `aarch64`.
- Normalize date-based Git tags for package versions so tags such as `v26.07.17-1` remain upgradeable on both Debian and Arch, while `pb version` retains the original Git tag and commit ID.
- Produce SHA-256 checksums and verify package installation with Debian/Ubuntu and Arch Linux containers before attaching artifacts to a GitHub Release.
- Extend the existing Docker-tag-to-Release workflow so CLI packages use the same release tag and are attached to the existing GitHub Release.
- Keep CLI/package documentation in root `package.md` and `package_ko.md`; the main English and Korean READMEs should contain only concise links to the matching package documents.
- Install the CLI binary at `/usr/bin/pb` and package documentation under `/usr/share/doc/pastebox-cli/`.
