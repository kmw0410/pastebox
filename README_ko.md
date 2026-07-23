# Pastebox
curl 기반 파일 공유 서비스

[English](./README.md) | Korean

CLI 패키지와 사용법은 [pastebox-cli](https://github.com/kmw0410/pastebox-cli)에서 확인하세요.

![](./main.png)

<details>
<summary>미리보기</summary>

### Paste

![](./paste.png)

### 비밀번호 보호

![](./password-protected.png)

### 관리자 페이지

![](./admin.png)

### 404

![](./404.png)
</details>

### 기술 스택
| 레이어 | 스택 |
|--------|------|
| OS | Alpine Linux 3.24.1 (미러: https://mirror5.krfoss.org/alpine) |
| 언어 | Go 1.26.4 |
| 프론트엔드 | Go HTML 템플릿 |
| 백엔드 | Go 표준 라이브러리 기반 HTTP 서버 |
| 저장소 | 로컬 / MySQL & MariaDB |
| 컨테이너 | Docker + Docker Compose |

*사용하고 싶은 Alpine 미러가 따로 있다면 Dockerfile에서 수정할 수 있습니다.*

### 디렉토리 구조
```text
pastebox/
├── .github/
│   └── workflows/
│       ├── docker-publish.yml
│       └── release.yml
├── AGENTS.md
├── DATA_POLICY.md
├── DATA_POLICY_ko.md
├── Dockerfile
├── LICENSE
├── README.md
├── README_ko.md
├── 404.png
├── admin.png
├── docker-compose.yml
├── docker-entrypoint.sh
├── go.mod
├── go.sum
├── main.png
├── paste.png
├── password-protected.png
├── cmd/
│   └── server/
│       ├── admin.go
│       ├── app.go
│       ├── auth.go
│       ├── handler_clone.go
│       ├── handler_index.go
│       ├── handler_manage.go
│       ├── handler_paste.go
│       ├── handler_upload.go
│       ├── i18n.go
│       ├── logging.go
│       ├── main.go
│       ├── main_test.go
│       ├── routes.go
│       ├── upload_validation.go
│       └── util.go
├── internal/
│   ├── admin_store.go
│   ├── locks.go
│   ├── metadata.go
│   ├── mysql_store.go
│   ├── secrets.go
│   ├── store.go
│   ├── store_local.go
│   └── store_test.go
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
    └── paste.html
```

### 어떻게 사용하나요?
> [!IMPORTANT]
> Timezone 기본값이 Asia/Seoul로 되어있습니다. 현재 거주하는 국가에 맞게 설정하세요.

1. 저장소를 클론하거나 `.zip` 파일로 다운로드하세요.
2. Docker compose를 사용하여 서비스를 구동하세요. `docker compose up -d --build`로 로컬 빌드 후 실행할 수도 있으며, GHCR의 미리 빌드된 이미지를 사용할 수도 있습니다. 빌드된 이미지를 사용하려면 `docker-compose.yml`을 사용하세요.
3. `http://localhost:3000`를 브라우저에서 접속하거나 NGINX, Caddy, Traefik을 통해 리버스 프록시를 구축하여 도메인으로 접속하세요. 정상적으로 구동이 되었다면 `curl`을 사용하여 이용할 수 있습니다.

### Healthcheck
Pastebox는 컨테이너와 로드밸런서 상태 확인용으로 `GET /healthz`와 `HEAD /healthz`를 제공합니다.

- 정상 상태면 `200 OK`와 `ok`를 반환합니다.
- 비정상 상태면 `503 Service Unavailable`를 반환합니다.
- 이 체크는 항상 관리자용 SQLite 데이터베이스를 확인합니다.
- `STORAGE_BACKEND=mysql`일 때는 설정된 MySQL 또는 MariaDB 연결도 함께 확인합니다.
- 업로드 비활성화 상태는 비정상으로 간주하지 않습니다.

제공된 Compose 파일에는 이미 `http://127.0.0.1:8080/healthz`를 조회하는 Docker healthcheck가 포함되어 있습니다.

예시:

```bash
curl -i http://localhost:8080/healthz
```

서버가 시작된 뒤 DB 연결이 끊기면 Pastebox는 컨테이너 로그에 상태 전환 로그도 남깁니다. 같은 장애를 매번 반복 기록하지 않고, 최초 장애 시점과 복구 시점만 기록합니다.

대표 로그 예시:

```text
store health check failed: backend=mysql error=dial tcp 10.0.0.10:3306: connect: connection refused
store health recovered: backend=mysql
```

### 스토리지 백엔드
Pastebox는 `local`과 `mysql` 스토리지 백엔드를 지원합니다. `local`은 로컬 파일 저장소에 사용하고, `mysql`은 외부 MySQL & MariaDB 데이터베이스에 사용합니다.

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `STORAGE_BACKEND` | `local` | Paste 저장 백엔드입니다. `/paste-data` 파일 저장은 `local`, 외부 MySQL & MariaDB 저장은 `mysql`을 사용합니다. |
| `MYSQL_DSN` | 비어 있음 | `STORAGE_BACKEND=mysql`일 때 필요합니다. 호환성을 위해 `parseTime=true`와 `utf8mb4` 옵션을 유지하세요. |
| `DB_ZSTD_LEVEL` | `3` | DB 모드 Paste 본문에 적용되는 선택적 zstd 압축 레벨입니다. |
| `MIGRATE_LOCAL_PASTES` | `false` | `STORAGE_BACKEND=mysql`일 때 기존 로컬 Paste 파일을 시작 중 MySQL로 이관합니다. 이 작업은 1회성 마이그레이션이며, 성공하면 완료 표시가 SQLite에 저장되어 이후 재시작에서는 건너뜁니다. |
| `MIGRATE_SQLITE_ADMIN_ACCOUNTS` | `false` | `STORAGE_BACKEND=mysql`일 때 기존 SQLite 관리자 계정, 관리자 세션, 관리자 설정을 시작 중 MySQL로 이관합니다. 이 작업은 1회성 마이그레이션이며, 성공하면 완료 표시가 SQLite에 저장되어 이후 재시작에서는 건너뜁니다. |

MySQL & MariaDB 설정 예시:

```yaml
environment:
  STORAGE_BACKEND: "mysql"
  MYSQL_DSN: "pastebox:pastebox@tcp(mysql.example.com:3306)/pastebox?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci"
  DB_ZSTD_LEVEL: "3"
  MIGRATE_LOCAL_PASTES: "true"
  MIGRATE_SQLITE_ADMIN_ACCOUNTS: "true"
```

`local` 모드에서는 Paste 본문이 `DATA_DIR` 아래 파일로 저장되고 메타데이터는 JSON sidecar 파일로 저장됩니다. `mysql` 모드에서는 Paste 본문과 관리자 계정, 관리자 세션, 관리자 설정이 설정된 MySQL 또는 MariaDB 데이터베이스에 저장됩니다. SQLite(`/paste-data/pastebox.db`)는 마이그레이션 완료 표시만 보관합니다.

`./data:/paste-data` 같은 bind mount를 사용할 때 호스트 사용자 계정이 저장 파일에 직접 접근해야 하면 `UID`와 `GID`를 호스트 사용자의 숫자 UID/GID로 맞춰 주세요. 예를 들어 `UID=1000`, `GID=1000`으로 설정하면 bind-mounted 데이터가 컨테이너 root 전용처럼 굳지 않고 해당 호스트 사용자 기준으로 기록됩니다.

두 마이그레이션 플래그는 기존 로컬 설치를 MySQL로 옮길 때만 쓰는 시작 시점 전용 도구입니다.

- `MIGRATE_LOCAL_PASTES=true`는 `DATA_DIR` 안의 로컬 Paste 파일과 JSON sidecar를 찾아 각 Paste를 MySQL로 복사한 뒤, 복사가 끝난 개별 항목부터 로컬 파일과 메타데이터를 삭제합니다.
- `MIGRATE_SQLITE_ADMIN_ACCOUNTS=true`는 기존 SQLite 관리자 계정, 관리자 세션, 관리자 설정을 MySQL로 복사한 뒤, 성공하면 이관된 SQLite 행을 삭제합니다. 마이그레이션 완료 표시는 계속 SQLite에 남습니다.

두 마이그레이션 모두 `pastebox_settings`의 완료 표시로 보호됩니다. 한 번 성공해서 완료 표시가 저장되면 이후 재시작에서는 다시 실행되지 않습니다. 마이그레이션이 완료 표시를 남기기 전에 실패하면 다음 시작 때 다시 시도합니다.

### 기능

> [!NOTE]
> **현재 사용 중인 도메인 또는 IP 주소에 맞게 `localhost`를 반드시 변경하세요.**

1. **파일 자동 삭제**: 업로드 시점 기준 30일 후 자동 삭제됩니다.

2. **텍스트 업로드**: **echo, cat (cat << EOF)**와 같은 Linux 명령어와 연계하여 텍스트를 직접 업로드할 수 있습니다.

   ```bash
   echo "hello" | curl -X POST --data-binary @- http://localhost:8080/
   ```

3. **파일 업로드**: `multipart/form-data` 형식의 파일 업로드를 지원합니다.

   ```bash
   curl -F "file=@test.txt" http://localhost:8080/
   ```

4. **영구 저장**: `data-policy: permanent` 헤더를 사용하면 자동 삭제 대상에서 제외되어 영구 저장됩니다.

   ```bash
   curl -H "data-policy: permanent" -F "file=@test.txt" http://localhost:8080/
   ```
    
   ```json
   // 저장 경로: ./data/코드.json

   {
     "id": "code",
     "created_at": "2026-05-25T06:46:51.108540924Z",
     "expires_at": "0001-01-01T00:00:00Z",
     "data_policy": "permanent",
     "size": 5,
     "content_type": "application/octet-stream"
   }
   ```

5. **일회성 저장**: `data-policy: once` 헤더를 사용하면 일회성으로 저장되며 사용자가 확인한 경우 자동으로 삭제됩니다.

   ```bash
   curl -H "data-policy: once" -F "file=@test.txt" http://localhost:8080/
   ```
    
   ```json
   {
    "id": "code",
    "delete_token_hash": "yourDeleteToken",
    "created_at": "2026-05-26T11:11:09.799454368Z",
    "expires_at": "2026-06-25T11:11:09.799454368Z",
    "data_policy": "once",
    "size": 6,
    "content_type": "text/plain; charset=utf-8"
   }
   ```

6. **사용자 지정 만료**: `data-policy` 값에 `30m`, `12h`, `7d` 같은 형식을 사용하면 최대 30일 이내로 만료 시간을 지정할 수 있습니다. 접미사는 분 단위 `m`, 시간 단위 `h`, 일 단위 `d`를 지원합니다. `0m`, `31d`, `721h`, `1w`, `1.5h` 같은 잘못된 값은 `400 Bad Request`로 거절됩니다.

   ```bash
   curl -H "data-policy: 12h" -F "file=@test.txt" http://localhost:8080/
   ```

7. **만료 시간 표시**: 일반 업로드와 사용자 지정 만료 업로드는 응답에 `expires` 항목이 포함되어 만료 시간을 확인할 수 있습니다. 응답 시간은 서버 로컬 시간대로 표시됩니다. `data-policy: permanent`를 사용한 경우에는 만료 시간이 표시되지 않습니다.

   ```
   url: http://localhost:8080/RANDOM_CODE
   expires: 2026-06-24T14:10:26+09:00
   manage: http://localhost:8080/RANDOM_CODE?manage=MANAGE_TOKEN
   ```

   스크립트나 다른 도구에서 파싱할 수 있도록 JSON 응답이 필요하면 업로드 요청에 `?format=json`을 붙이면 됩니다.

   ```bash
   curl -F "file=@test.txt" "http://localhost:8080/?format=json"
   ```

   ```json
   {
     "url": "http://localhost:8080/RANDOM_CODE",
     "expires": "2026-06-24T14:10:26+09:00",
     "password": "RANDOM_PASSWORD",
     "manage": "http://localhost:8080/RANDOM_CODE?manage=MANAGE_TOKEN",
     "password_protected": true
   }
   ```

8. **수동 삭제**: 업로드 응답의 비공개 관리 URL을 열고 삭제 버튼을 사용합니다. 관리 페이지는 관리 API에 인증된 `DELETE` 요청을 보냅니다. CLI 클라이언트도 관리 토큰으로 같은 요청을 보낼 수 있으며, 삭제 요청은 컨테이너 로그에도 기록됩니다.

   ```bash
   curl -X DELETE -H "paste-manage-token: MANAGE_TOKEN" http://localhost:8080/api/v1/pastes/RANDOM_CODE
   ```

9. **비밀번호 링크**: `usepassword: true` 헤더를 사용한 비공개 업로드 링크 생성을 지원합니다. 헤더 사용 시 **영문 대문자 + 영문 소문자 + 숫자 + 특수문자** 조합으로 생성된 8자리 비밀번호가 발급됩니다. 파일은 `?password=...` 쿼리 파라미터 또는 `paste-password: ...` 헤더를 사용하여 바로 확인하거나 브라우저에서 접근 시 직접 비밀번호를 입력하여 확인할 수 있습니다.

   ```bash
   # 비밀번호 링크 생성
   curl -H "usepassword: true" -F "file=@secret.txt" http://localhost:8080/

   # 파일 확인: 헤더 방식
   curl -H "paste-password: RANDOM_PASSWORD" http://localhost:8080/RANDOM_CODE

   # 파일 확인: 쿼리 파라미터 방식
   curl "http://localhost:8080/RANDOM_CODE?password=RANDOM_PASSWORD"
   ```

   대화형 클라이언트는 대신 `password` 헤더로 8~128자 비밀번호를 전달할 수 있습니다. `usepassword: true`와 함께 사용할 수 없으며, 전달된 비밀번호는 해시로만 저장되고 업로드 응답에 다시 노출되지 않습니다. JSON 업로드 및 복제 응답에는 클라이언트가 보호 적용 여부를 확인할 수 있도록 `password_protected`가 포함됩니다.

10. **사용자 지정 코드**: `code: ...` 헤더를 사용하면 무작위로 생성된 코드 대신 원하는 코드를 사용하여 링크를 만들 수 있습니다. **영문 대문자와 소문자, 숫자, 특수 문자 `_` 및 `-`를 지원합니다.** 10자를 초과하는 코드나 중복된 코드는 생성할 수 없습니다.

   ```bash
   curl -H "code: custom123" -F "file=@secret.txt" http://localhost:8080/
   ```

   선택 사항인 `label: ...` 헤더를 사용하면 코드 및 원본 파일명과 별개로 Paste를 구분할 수 있습니다. 라벨은 최대 100자이며 clone 시 유지되고 비공개 관리 페이지에서 변경할 수 있습니다.

   ```bash
   curl -H "label: 운영 배포 로그" -F "file=@server.log" http://localhost:8080/
   ```
   
11. **업로드 응답 형식**: 업로드가 성공하면 URL, 만료 시간, 비공개 관리 링크가 반환됩니다. 생성 비밀번호를 사용하는 경우 `password` 항목도 함께 반환됩니다.

   ```
   url: http://localhost:8080/RANDOM_CODE
   expires: 2026-06-24T14:10:26+09:00
   password: RANDOM_PASSWORD
   manage: http://localhost:8080/RANDOM_CODE?manage=MANAGE_TOKEN
   ```

12. **Paste 관리 링크**: 모든 성공한 업로드는 비공개 관리 URL도 함께 생성합니다. 이 링크는 `?manage=...` 쿼리 파라미터 형태로 업로드 응답에 포함되며, 비밀번호 입력 없이 바로 관리 페이지에 접근할 수 있습니다. 이후에도 관리 페이지를 열 때는 동일한 `manage` 토큰을 URL에 포함해야 합니다.

   관리 링크로 할 수 있는 작업은 다음과 같습니다.

   - 공개 / 비밀번호 보호 전환
   - 보관 정책 변경
   - Paste 삭제

   비밀번호 보호 Paste를 다시 공개로 돌릴 때는, 현재 생성된 비밀번호를 먼저 입력하여 검증해야 합니다.

   CLI 및 자동화 클라이언트는 `/api/v1/pastes/<code>`의 버전 지정 JSON API를 사용할 수 있습니다. 비공개 관리 토큰은 `paste-manage-token` 헤더로 전달하며 이 API 응답에는 토큰이 포함되지 않습니다. `GET`은 관리 메타데이터를 반환하고, `PATCH`는 `set_label`, `set_policy`, `enable_password`, `disable_password` 작업을 처리하며, `DELETE`는 Paste를 삭제합니다.

   ```bash
   curl -H "paste-manage-token: MANAGE_TOKEN" http://localhost:8080/api/v1/pastes/RANDOM_CODE
   curl -X PATCH -H "Content-Type: application/json" -H "paste-manage-token: MANAGE_TOKEN" --data '{"action":"set_policy","data_policy":"12h"}' http://localhost:8080/api/v1/pastes/RANDOM_CODE
   curl -X DELETE -H "paste-manage-token: MANAGE_TOKEN" http://localhost:8080/api/v1/pastes/RANDOM_CODE
   ```

13. **브라우저에서 내용 복사**: 텍스트 기반 업로드 링크를 브라우저에서 열면 `Raw` 버튼 옆의 `Copy` 버튼으로 내용을 클립보드에 복사할 수 있습니다.

14. **텍스트 파일 브라우저 표시**: `.txt`, `.log` 같은 텍스트 기반 파일은 다운로드되지 않고 브라우저에서 바로 표시됩니다. 원본 raw 응답이 필요하면 `?format=raw`를 사용할 수 있습니다.

15. **생성/삭제 로그**: 파일 생성 및 삭제 시 컨테이너 로그에 기록됩니다.

   ```
   created: id=AbC12 remote=127.0.0.1:51234 size=123 content_type="text/plain; charset=utf-8" policy=temporary expires=2026-06-24T05:10:26Z protected=false
   deleted: id=AbC12 remote=127.0.0.1:51234
   ```

16. **세분화된 락 매니저**: 업로드 ID별로 락을 적용하여 같은 파일에 대한 조회, 삭제, 만료 정리 작업이 동시에 발생해도 충돌을 줄입니다. 서로 다른 파일은 병렬로 처리됩니다.

17. **관리 페이지 제공**: IP, 도메인 뒤에 `/admin`을 추가하여 관리페이지 접근이 가능합니다. 계정이 없는 경우 첫 생성된 계정이 관리자로 들어가며 이후 신규생성이 중단됩니다. `STORAGE_BACKEND=mysql`일 때는 관리자 계정과 관리자 세션, 관리자 설정이 MySQL에 저장됩니다. 이 경우 SQLite(`/paste-data/pastebox.db`, 호스트의 경우 `./data/pastebox.db`)는 마이그레이션 완료 표시만 보관합니다. 비밀번호는 해시되어 저장됩니다. 관리 대시보드에서는 Paste 개수, 저장 용량, 정책별 분포, 24시간 이내 만료 항목, 만료된 항목, 현재 Paste 저장 백엔드를 보여줍니다. 또한 업로드 비활성화 기능과 개별 Paste 삭제, 선택된 Paste 일괄 삭제를 지원합니다.

18. **관리자 비밀번호 초기화**: 관리자 비밀번호를 분실한 경우 `docker-compose.yml`에 `ADMIN_RESET_TOKEN`을 설정한 뒤 컨테이너를 재시작하고 `/admin/reset`에 접속하세요. 초기화 토큰과 새 비밀번호를 입력하면 비밀번호가 재설정되며, 기존 관리자 세션은 모두 만료되어 새 비밀번호로 다시 로그인해야 합니다.

19. **Paste 관리 링크**: 모든 성공한 업로드는 `?manage=...` 형식의 비공개 관리 URL도 함께 생성합니다. 이 링크로 관리 페이지에 바로 들어갈 수 있으며, 공개 / 비밀번호 보호 전환, 보관 정책 변경, Paste 삭제를 할 수 있습니다. 비밀번호 보호 Paste를 다시 공개로 바꿀 때는 먼저 현재 생성된 비밀번호를 확인합니다.

20. **문법 강조 지원**: `.txt`, `.md`, `.log`, `.csv`, `.conf`, `.yaml`, `.toml`, `.go`, `.rs`, `.js`, `.py`, `.ts`, `.php`, `.html`, `.css`, `.sql`, `.lua`, `.sh` 같은 자주 쓰는 텍스트 형식에 문법 강조를 지원합니다. `Dockerfile`, `*.Dockerfile`, `Makefile`, `.env.example`, `.gitignore`, `compose.yaml`, `compose.yml`, `docker-compose.yaml`, `docker-compose.yml`, `nginx.conf`, `*.nginx.conf`도 파일명 기준으로 감지합니다.

21. **긴 한 줄 Wrap 모드**: Paste에 매우 긴 한 줄이 포함되어 있으면 보기 페이지에 `Long line detected` 힌트와 `Wrap` 버튼이 표시되어, 가로 스크롤 대신 줄바꿈된 읽기 모드로 전환할 수 있습니다.

22. **Paste 복제 지원**: 보기 페이지에서 `Clone` 버튼을 눌러 현재 Paste 내용을 새로운 링크로 복제할 수 있습니다.

23. **Open Graph 메타데이터**: Paste 페이지는 `Pastebox - 코드` 형식의 미리보기 제목을 제공합니다. 공개 Paste는 원본 파일명이 있으면 미리보기 설명으로 사용하고, 비밀번호 보호 Paste는 `Password-protected paste`만 표시하여 파일명, 라벨, 내용, 비밀번호 및 관리 토큰을 노출하지 않습니다.

### 데이터 정책
데이터 정책 헤더에 대한 설명은 [DATA_POLICY_ko.md](./DATA_POLICY_ko.md)를 참고하세요.
