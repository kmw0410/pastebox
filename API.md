# Pastebox HTTP API

English | [Korean](./API_ko.md)

This document describes the public HTTP interface implemented by Pastebox. Replace `https://paste.example.com` with the base URL of your deployment.

## Overview

| Operation | Method and path | Authentication |
|---|---|---|
| Upload | `POST /` or `PUT /` | None |
| Read a paste | `GET /<code>` or `HEAD /<code>` | `paste-password` when protected |
| Clone a paste | `POST /<code>` | `paste-password` when the source is protected |
| Read management metadata | `GET /api/v1/pastes/<code>` | `paste-manage-token` |
| Update a paste | `PATCH /api/v1/pastes/<code>` | `paste-manage-token` |
| Delete a paste | `DELETE /api/v1/pastes/<code>` | `paste-manage-token` |

Paste content is text-only and uploads are limited to 1 GiB. API clients should treat generated passwords and private manage URLs as secrets. Do not put them in logs.

## Upload

Use a streaming raw request body for stdin or a multipart `file` field when the original filename must be preserved.

```bash
# Raw text
printf 'hello\n' | curl -X POST --data-binary @- https://paste.example.com/

# File upload with original filename
curl -F "file=@server.log" https://paste.example.com/
```

### Upload headers

| Header | Value |
|---|---|
| `data-policy` | `temporary` (default), `permanent`, `once`, or a duration such as `30m`, `12h`, or `7d`, up to 30 days |
| `usepassword` | `true` to generate an 8-character password |
| `password` | A custom password containing 8–128 characters and no control characters |
| `code` | A custom code of 1–10 characters using only `A-Z`, `a-z`, `0-9`, `_`, and `-` |
| `label` | An optional label of up to 100 characters without control characters |

`usepassword: true` and the `password` header are mutually exclusive.

The default response is plain text:

```text
url: https://paste.example.com/AbC12
expires: 2026-07-24T12:00:00+09:00
password: GENERATED_PASSWORD
manage: https://paste.example.com/AbC12?manage=MANAGE_TOKEN
```

`expires` is omitted for permanent pastes. `password` is returned only when Pastebox generated it. A client-supplied password is never echoed.

Append `?format=json` for JSON:

```bash
curl -F "file=@server.log" "https://paste.example.com/?format=json"
```

```json
{
  "url": "https://paste.example.com/AbC12",
  "expires": "2026-07-24T12:00:00+09:00",
  "manage": "https://paste.example.com/AbC12?manage=MANAGE_TOKEN",
  "password_protected": false
}
```

The upload response does not include a separate delete URL. Delete the paste from its private manage page or with the management API.

Common upload errors include `400 Bad Request` for invalid headers or multipart input, `409 Conflict` for a duplicate custom code, `413 Payload Too Large`, `415 Unsupported Media Type`, and `503 Service Unavailable` when uploads are disabled. With `?format=json`, errors use `{"error":"message"}`.

## Read a paste

Request the original content with `?raw=1`. Send protected-paste passwords in the `paste-password` header so they do not appear in URLs or access logs.

```bash
curl "https://paste.example.com/AbC12?raw=1"
curl -H "paste-password: PASTE_PASSWORD" "https://paste.example.com/AbC12?raw=1"
```

`HEAD /<code>` checks access and returns headers without consuming a one-time paste. A successful `GET` of a one-time paste deletes it after that view. Missing, expired, or already consumed pastes return `404 Not Found`; an invalid or missing password returns `401 Unauthorized` for non-browser clients.

## Clone a paste

Clone with `POST /<source-code>`. The clone preserves the source filename and label. Upload options such as `data-policy`, `usepassword`, `password`, and `code` apply to the new paste.

```bash
curl -X POST \
  -H "paste-password: SOURCE_PASSWORD" \
  -H "data-policy: 12h" \
  "https://paste.example.com/AbC12?format=json"
```

The clone response uses the same plain-text or JSON schema as upload and returns a new private manage URL. Cloning returns `503 Service Unavailable` when uploads are disabled.

## Management API

The versioned management endpoint is:

```text
/api/v1/pastes/<code>
```

Extract the `manage` query value from the private manage URL returned at upload or clone time, then send it in the `paste-manage-token` header. Management API responses are JSON and use `Cache-Control: no-store`.

```bash
MANAGE_TOKEN='token-from-the-manage-url'
```

### Get metadata

```bash
curl -H "paste-manage-token: $MANAGE_TOKEN" \
  https://paste.example.com/api/v1/pastes/AbC12
```

```json
{
  "id": "AbC12",
  "filename": "server.log",
  "label": "production",
  "url": "https://paste.example.com/AbC12",
  "created_at": "2026-07-23T10:00:00+09:00",
  "expires": "2026-07-24T10:00:00+09:00",
  "data_policy": "temporary",
  "size": 1234,
  "content_type": "text/x-log; charset=utf-8",
  "password_protected": false
}
```

`filename`, `label`, and `expires` are omitted when they have no value. Token hashes and other private storage metadata are never returned.

### Update metadata or protection

Send one JSON object with an `action`. Unknown fields, malformed JSON, trailing JSON values, and bodies larger than 64 KiB are rejected.

Set a label:

```bash
curl -X PATCH \
  -H "Content-Type: application/json" \
  -H "paste-manage-token: $MANAGE_TOKEN" \
  --data '{"action":"set_label","label":"production"}' \
  https://paste.example.com/api/v1/pastes/AbC12
```

Set a retention policy:

```json
{"action":"set_policy","data_policy":"12h"}
```

Enable protection with a generated password:

```json
{"action":"enable_password"}
```

The generated password is returned once in the successful response as `password`.

Enable protection with a client-supplied password:

```json
{"action":"enable_password","new_password":"a-custom-password"}
```

The supplied password is stored as a hash and is not returned.

Disable protection by proving knowledge of the current password:

```json
{"action":"disable_password","password":"current-password"}
```

Every successful PATCH returns the current metadata using the same schema as GET.

### Delete

```bash
curl -X DELETE \
  -H "paste-manage-token: $MANAGE_TOKEN" \
  https://paste.example.com/api/v1/pastes/AbC12
```

```json
{
  "deleted": true,
  "id": "AbC12"
}
```

The browser manage page uses this same DELETE request.

For backward compatibility, DELETE also accepts a previously issued token in `paste-delete-token`. Send exactly one of `paste-manage-token` or `paste-delete-token`. New upload and clone responses no longer issue delete URLs, so new clients should use the manage token.

### Management API errors

Errors always use this JSON shape:

```json
{"error":"message"}
```

| Status | Meaning |
|---|---|
| `400 Bad Request` | Invalid JSON, action, label, policy, password, or token-header combination |
| `401 Unauthorized` | The current paste password is missing or incorrect when disabling protection |
| `404 Not Found` | The paste does not exist, has expired, or the private token is invalid |
| `409 Conflict` | Password protection is already enabled |
| `405 Method Not Allowed` | Unsupported method; the endpoint allows `GET`, `PATCH`, and `DELETE` |
| `500 Internal Server Error` | The request could not be completed |

## Health check

`GET /healthz` and `HEAD /healthz` return `200 OK` with `ok` while required storage is healthy, or `503 Service Unavailable` when it is not.
