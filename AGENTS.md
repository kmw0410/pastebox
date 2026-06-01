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
    ├── clone.html
    ├── index.html
    ├── password.html
    ├── paste.html
    └── (optional design prototypes)
```

## 3. Architecture
- HTTP server: `cmd/server/main.go`
  - Routing, upload handling, paste view/raw view, password checks, clone flow, admin auth and controls, template rendering, i18n loading.
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
- Password-protected: `usepassword: true`.

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
- `expires` (when applicable; omitted for permanent)
- `delete`
- `password` (when applicable)

Delete token handling:
- Never log delete tokens.
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

## 8. Clone Behavior
- Clone must be triggered from the paste view page UX.
- Do not require users to manually use `/clone` or `?clone=1` as a primary UX.
- Clone must preserve original filename.
- Clone must be blocked when uploads are disabled.
- Clone result page (`templates/clone.html`) should show URL, expiration (if any), generated password (if any), and delete URL.

## 9. Admin Behavior
Admin entry point: `/admin`.

Admin account rules:
- If no admin exists, the first created account becomes the only administrator account.
- Additional admin registration must remain disabled afterward.
- Admin passwords must never be stored in plain text.

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
- Index page should use:
```html
<script src="https://cdn.jsdelivr.net/npm/@tailwindcss/browser@4"></script>
```
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
- Admin passwords
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

## 17. Commit Message Examples
Use short, conventional commit messages:
```text
feat: add language support
feat: show original filenames
feat: add admin delete controls
fix: keep line numbers on the left
fix: restore localized index page
perf: stream uploads after text sample validation
```

## 18. Final Guidance
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
