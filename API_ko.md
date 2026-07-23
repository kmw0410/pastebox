# Pastebox HTTP API

[English](./API.md) | Korean

이 문서는 Pastebox가 제공하는 공개 HTTP 인터페이스를 설명합니다. 예시의 `https://paste.example.com`은 실제 배포 주소로 변경하세요.

## 개요

| 작업 | 메서드와 경로 | 인증 |
|---|---|---|
| 업로드 | `POST /` 또는 `PUT /` | 없음 |
| Paste 조회 | `GET /<code>` 또는 `HEAD /<code>` | 보호된 경우 `paste-password` |
| Paste 복제 | `POST /<code>` | 원본이 보호된 경우 `paste-password` |
| 관리 메타데이터 조회 | `GET /api/v1/pastes/<code>` | `paste-manage-token` |
| Paste 변경 | `PATCH /api/v1/pastes/<code>` | `paste-manage-token` |
| Paste 삭제 | `DELETE /api/v1/pastes/<code>` | `paste-manage-token` |

Paste 본문은 텍스트만 허용되며 업로드 한도는 1 GiB입니다. 생성된 비밀번호와 비공개 관리 URL은 비밀 정보로 취급하고 로그에 남기지 마세요.

## 업로드

stdin은 스트리밍 raw body로 전송하고, 원본 파일명을 보존해야 하는 파일은 multipart `file` 필드로 전송합니다.

```bash
# Raw 텍스트
printf 'hello\n' | curl -X POST --data-binary @- https://paste.example.com/

# 원본 파일명을 보존하는 파일 업로드
curl -F "file=@server.log" https://paste.example.com/
```

### 업로드 헤더

| 헤더 | 값 |
|---|---|
| `data-policy` | `temporary`(기본값), `permanent`, `once` 또는 최대 30일 이내의 `30m`, `12h`, `7d` 같은 기간 |
| `usepassword` | 8자리 비밀번호를 생성하려면 `true` |
| `password` | 제어 문자가 없는 8–128자의 사용자 지정 비밀번호 |
| `code` | `A-Z`, `a-z`, `0-9`, `_`, `-`만 사용하는 1–10자 사용자 지정 코드 |
| `label` | 제어 문자가 없는 최대 100자의 선택적 라벨 |

`usepassword: true`와 `password` 헤더는 함께 사용할 수 없습니다.

기본 응답은 일반 텍스트입니다.

```text
url: https://paste.example.com/AbC12
expires: 2026-07-24T12:00:00+09:00
password: GENERATED_PASSWORD
manage: https://paste.example.com/AbC12?manage=MANAGE_TOKEN
```

영구 Paste에는 `expires`가 없습니다. `password`는 Pastebox가 생성한 경우에만 반환되며 클라이언트가 전달한 비밀번호는 응답에 포함되지 않습니다.

JSON 응답이 필요하면 `?format=json`을 추가합니다.

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

업로드 응답은 별도 삭제 URL을 포함하지 않습니다. 비공개 관리 페이지 또는 관리 API에서 Paste를 삭제하세요.

대표적인 업로드 오류는 잘못된 헤더나 multipart 입력의 `400 Bad Request`, 중복 사용자 지정 코드의 `409 Conflict`, `413 Payload Too Large`, `415 Unsupported Media Type`, 업로드 비활성화 상태의 `503 Service Unavailable`입니다. `?format=json`을 사용하면 오류는 `{"error":"message"}` 형식입니다.

## Paste 조회

원문은 `?raw=1`로 요청합니다. 보호된 Paste의 비밀번호는 URL이나 접근 로그에 남지 않도록 `paste-password` 헤더로 전달하세요.

```bash
curl "https://paste.example.com/AbC12?raw=1"
curl -H "paste-password: PASTE_PASSWORD" "https://paste.example.com/AbC12?raw=1"
```

`HEAD /<code>`는 접근 가능 여부와 헤더만 확인하며 일회성 Paste를 소비하지 않습니다. 일회성 Paste는 성공한 첫 `GET` 조회 후 삭제됩니다. 없거나 만료되었거나 이미 소비된 Paste는 `404 Not Found`, 비브라우저 클라이언트가 잘못된 비밀번호를 보내거나 생략하면 `401 Unauthorized`를 반환합니다.

## Paste 복제

`POST /<source-code>`로 복제합니다. 원본 파일명과 라벨은 유지되며 새 Paste에는 `data-policy`, `usepassword`, `password`, `code` 같은 업로드 옵션을 적용할 수 있습니다.

```bash
curl -X POST \
  -H "paste-password: SOURCE_PASSWORD" \
  -H "data-policy: 12h" \
  "https://paste.example.com/AbC12?format=json"
```

복제 응답은 업로드와 같은 일반 텍스트 또는 JSON 구조를 사용하며 새 비공개 관리 URL을 반환합니다. 업로드가 비활성화된 상태에서는 복제도 `503 Service Unavailable`로 거절됩니다.

## 관리 API

버전이 지정된 관리 엔드포인트는 다음과 같습니다.

```text
/api/v1/pastes/<code>
```

업로드 또는 복제 응답의 비공개 관리 URL에서 `manage` 쿼리 값을 추출하여 `paste-manage-token` 헤더로 전달합니다. 관리 API 응답은 JSON이며 `Cache-Control: no-store`가 적용됩니다.

```bash
MANAGE_TOKEN='관리-URL에서-추출한-토큰'
```

### 메타데이터 조회

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

값이 없으면 `filename`, `label`, `expires`는 생략됩니다. 토큰 해시를 비롯한 비공개 저장 메타데이터는 반환되지 않습니다.

### 메타데이터 또는 보호 설정 변경

`action`이 포함된 JSON 객체 하나를 전송합니다. 알 수 없는 필드, 잘못된 JSON, 뒤에 다른 JSON 값이 붙은 요청, 64 KiB를 초과하는 본문은 거절됩니다.

라벨 설정:

```bash
curl -X PATCH \
  -H "Content-Type: application/json" \
  -H "paste-manage-token: $MANAGE_TOKEN" \
  --data '{"action":"set_label","label":"production"}' \
  https://paste.example.com/api/v1/pastes/AbC12
```

보관 정책 설정:

```json
{"action":"set_policy","data_policy":"12h"}
```

생성 비밀번호로 보호 활성화:

```json
{"action":"enable_password"}
```

생성된 비밀번호는 성공 응답의 `password`로 한 번만 반환됩니다.

사용자 지정 비밀번호로 보호 활성화:

```json
{"action":"enable_password","new_password":"a-custom-password"}
```

전달한 비밀번호는 해시로 저장되며 응답에 포함되지 않습니다.

현재 비밀번호를 확인한 뒤 보호 비활성화:

```json
{"action":"disable_password","password":"current-password"}
```

성공한 PATCH 요청은 모두 GET과 같은 구조의 현재 메타데이터를 반환합니다.

### 삭제

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

브라우저 관리 페이지의 삭제 버튼도 동일한 DELETE 요청을 사용합니다.

이전에 발급된 토큰과의 호환을 위해 DELETE는 `paste-delete-token`도 허용합니다. `paste-manage-token`과 `paste-delete-token` 중 정확히 하나만 전송해야 합니다. 새 업로드와 복제 응답은 삭제 URL을 발급하지 않으므로 새 클라이언트는 관리 토큰을 사용해야 합니다.

### 관리 API 오류

오류는 항상 다음 JSON 형식을 사용합니다.

```json
{"error":"message"}
```

| 상태 | 의미 |
|---|---|
| `400 Bad Request` | 잘못된 JSON, 작업, 라벨, 정책, 비밀번호 또는 토큰 헤더 조합 |
| `401 Unauthorized` | 보호 비활성화에 필요한 현재 Paste 비밀번호가 없거나 올바르지 않음 |
| `404 Not Found` | Paste가 없거나 만료되었거나 비공개 토큰이 올바르지 않음 |
| `409 Conflict` | 비밀번호 보호가 이미 활성화됨 |
| `405 Method Not Allowed` | 지원하지 않는 메서드이며 이 엔드포인트는 `GET`, `PATCH`, `DELETE`만 허용 |
| `500 Internal Server Error` | 요청을 완료하지 못함 |

## 상태 확인

필수 저장소가 정상이면 `GET /healthz`와 `HEAD /healthz`는 `200 OK`와 `ok`를 반환하며, 비정상이면 `503 Service Unavailable`를 반환합니다.
