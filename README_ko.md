# Pastebox
curl 기반 파일 공유 서비스

[English](./README.md) | Korean

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
| OS | Alpine Linux 3.23.4 (미러: https://mirror5.krfoss.org/alpine) |
| 언어 | Go |
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
├── docker-compose-build.yml
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

### 스토리지 백엔드
Pastebox는 `local`과 `mysql` 스토리지 백엔드를 지원합니다. `local`은 로컬 파일 저장소에 사용하고, `mysql`은 외부 MySQL & MariaDB 데이터베이스에 사용합니다.

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `STORAGE_BACKEND` | `local` | Paste 저장 백엔드입니다. `/paste-data` 파일 저장은 `local`, 외부 MySQL & MariaDB 저장은 `mysql`을 사용합니다. |
| `MYSQL_DSN` | 비어 있음 | `STORAGE_BACKEND=mysql`일 때 필요합니다. 호환성을 위해 `parseTime=true`와 `utf8mb4` 옵션을 유지하세요. |
| `DB_ZSTD_LEVEL` | `3` | DB 모드 Paste 본문에 적용되는 선택적 zstd 압축 레벨입니다. |
| `MIGRATE_LOCAL_PASTES` | `false` | `STORAGE_BACKEND=mysql`일 때 기존 로컬 Paste 파일을 시작 중 MySQL로 이관합니다. 이 작업은 1회성 마이그레이션이며, 성공하면 완료 표시가 SQLite에 저장되어 이후 재시작에서는 건너뜁니다. |
| `MIGRATE_SQLITE_ADMIN_ACCOUNTS` | `false` | `STORAGE_BACKEND=mysql`일 때 기존 SQLite 관리자 계정을 시작 중 MySQL로 이관합니다. 이 작업은 1회성 마이그레이션이며, 성공하면 완료 표시가 SQLite에 저장되어 이후 재시작에서는 건너뜁니다. |

MySQL & MariaDB 설정 예시:

```yaml
environment:
  STORAGE_BACKEND: "mysql"
  MYSQL_DSN: "pastebox:pastebox@tcp(mysql.example.com:3306)/pastebox?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci"
  DB_ZSTD_LEVEL: "3"
  MIGRATE_LOCAL_PASTES: "true"
  MIGRATE_SQLITE_ADMIN_ACCOUNTS: "true"
```

`local` 모드에서는 Paste 본문이 `DATA_DIR` 아래 파일로 저장되고 메타데이터는 JSON sidecar 파일로 저장됩니다. `mysql` 모드에서는 Paste 본문이 설정된 MySQL 또는 MariaDB 데이터베이스에 zstd 압축 chunk로 저장됩니다. 관리자 세션, 서비스 설정, 업로드 비활성화 상태는 계속 SQLite(`/paste-data/pastebox.db`)를 사용합니다.

두 마이그레이션 플래그는 기존 로컬 설치를 MySQL로 옮길 때만 쓰는 시작 시점 전용 도구입니다.

- `MIGRATE_LOCAL_PASTES=true`는 `DATA_DIR` 안의 로컬 Paste 파일과 JSON sidecar를 찾아 각 Paste를 MySQL로 복사한 뒤, 복사가 끝난 개별 항목부터 로컬 파일과 메타데이터를 삭제합니다.
- `MIGRATE_SQLITE_ADMIN_ACCOUNTS=true`는 기존 SQLite 관리자 계정을 MySQL로 복사한 뒤, 성공하면 SQLite 쪽 계정을 삭제합니다.

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

6. **만료 시간 표시**: 일반 업로드는 응답에 `expires` 항목이 포함되어 만료 시간을 확인할 수 있습니다. `data-policy: permanent`를 사용한 경우에는 만료 시간이 표시되지 않습니다.

   ```
   url: http://localhost:8080/RANDOM_CODE
   expires: 2026-06-24T05:10:26Z
   delete: http://localhost:8080/RANDOM_CODE?delete=DELETE_TOKEN
   ```

   스크립트나 다른 도구에서 파싱할 수 있도록 JSON 응답이 필요하면 업로드 요청에 `?format=json`을 붙이면 됩니다.

   ```bash
   curl -F "file=@test.txt" "http://localhost:8080/?format=json"
   ```

   ```json
   {
     "url": "http://localhost:8080/RANDOM_CODE",
     "expires": "2026-06-24T05:10:26Z",
     "password": "RANDOM_PASSWORD",
     "manage": "http://localhost:8080/RANDOM_CODE?manage=MANAGE_TOKEN",
     "delete": "http://localhost:8080/RANDOM_CODE?delete=DELETE_TOKEN"
   }
   ```

7. **수동 삭제**: 업로드 시 발급되는 삭제 URL을 사용하여 파일을 직접 삭제할 수 있습니다. 삭제 요청은 컨테이너 로그에도 기록됩니다.

   ```bash
   curl "http://localhost:8080/RANDOM_CODE?delete=DELETE_TOKEN"
   ```

8. **비밀번호 링크**: `usepassword: true` 헤더를 사용한 비공개 업로드 링크 생성을 지원합니다. 헤더 사용 시 **영문 대문자 + 영문 소문자 + 숫자 + 특수문자** 조합으로 생성된 8자리 비밀번호가 발급됩니다. 파일은 `?password=...` 쿼리 파라미터 또는 `paste-password: ...` 헤더를 사용하여 바로 확인하거나 브라우저에서 접근 시 직접 비밀번호를 입력하여 확인할 수 있습니다.

   ```bash
   # 비밀번호 링크 생성
   curl -H "usepassword: true" -F "file=@secret.txt" http://localhost:8080/

   # 파일 확인: 헤더 방식
   curl -H "paste-password: RANDOM_PASSWORD" http://localhost:8080/RANDOM_CODE

   # 파일 확인: 쿼리 파라미터 방식
   curl "http://localhost:8080/RANDOM_CODE?password=RANDOM_PASSWORD"
   ```

9. **사용자 지정 코드**: `code: ...` 헤더를 사용하면 무작위로 생성된 코드 대신 원하는 코드를 사용하여 링크를 만들 수 있습니다. **영문 대문자와 소문자, 숫자, 특수 문자 `_` 및 `-`를 지원합니다.** 10자를 초과하는 코드나 중복된 코드는 생성할 수 없습니다.

   ```bash
   curl -H "code: custom123" -F "file=@secret.txt" http://localhost:8080/
   ```
   
10. **업로드 응답 형식**: 업로드가 성공하면 URL, 만료 시간, 삭제 링크가 반환됩니다. 비밀번호 링크인 경우 `password` 항목도 함께 반환됩니다.

   ```
   url: http://localhost:8080/RANDOM_CODE
   expires: 2026-06-24T05:10:26Z
   password: RANDOM_PASSWORD
   manage: http://localhost:8080/RANDOM_CODE?manage=MANAGE_TOKEN
   delete: http://localhost:8080/RANDOM_CODE?delete=DELETE_TOKEN
   ```

11. **Paste 관리 링크**: 모든 성공한 업로드는 비공개 관리 URL도 함께 생성합니다. 이 링크는 `?manage=...` 쿼리 파라미터 형태로 업로드 응답에 포함되며, 비밀번호 입력 없이 바로 관리 페이지에 접근할 수 있습니다. 이후에도 관리 페이지를 열 때는 동일한 `manage` 토큰을 URL에 포함해야 합니다.

   관리 링크로 할 수 있는 작업은 다음과 같습니다.

   - 공개 / 비밀번호 보호 전환
   - 보관 정책 변경
   - Paste 삭제

   비밀번호 보호 Paste를 다시 공개로 돌릴 때는, 현재 생성된 비밀번호를 먼저 입력하여 검증해야 합니다.

12. **브라우저에서 내용 복사**: 텍스트 기반 업로드 링크를 브라우저에서 열면 `Raw` 버튼 옆의 `Copy` 버튼으로 내용을 클립보드에 복사할 수 있습니다.

13. **텍스트 파일 브라우저 표시**: `.txt`, `.log` 같은 텍스트 기반 파일은 다운로드되지 않고 브라우저에서 바로 표시됩니다. 원본 raw 응답이 필요하면 `?format=raw`를 사용할 수 있습니다.

14. **생성/삭제 로그**: 파일 생성 및 삭제 시 컨테이너 로그에 기록됩니다.

   ```
   event=paste.created content_type="text/plain; charset=utf-8" expires="2026-06-24T05:10:26Z" id="AbC12" policy="temporary" protected="false" remote="127.0.0.1:51234" size="123"
   event=paste.deleted id="AbC12" remote="127.0.0.1:51234"
   ```

15. **세분화된 락 매니저**: 업로드 ID별로 락을 적용하여 같은 파일에 대한 조회, 삭제, 만료 정리 작업이 동시에 발생해도 충돌을 줄입니다. 서로 다른 파일은 병렬로 처리됩니다.

16. **관리 페이지 제공**: IP, 도메인 뒤에 `/admin`을 추가하여 관리페이지 접근이 가능합니다. 계정이 없는 경우 첫 생성된 계정이 관리자로 들어가며 이후 신규생성이 중단됩니다. DB의 경우 `/paste-data/pastebox.db (호스트의 경우 ./data/pastebox.db)`에 기록되며 비밀번호의 경우 암호화되어 저장됩니다. 또한 업로드 비활성화 기능을 제공하여 신규 업로드를 중단할 수 있습니다.

17. **관리자 비밀번호 초기화**: 관리자 비밀번호를 분실한 경우 `docker-compose-build.yml`(또는 `docker-compose.yml`)에 `ADMIN_RESET_TOKEN`을 설정한 뒤 컨테이너를 재시작하고 `/admin/reset`에 접속하세요. 초기화 토큰과 새 비밀번호를 입력하면 비밀번호가 재설정되며, 기존 관리자 세션은 모두 만료되어 새 비밀번호로 다시 로그인해야 합니다.

18. **문법 강조 지원**: `.txt`, `.md`, `.log`, `.csv`, `.conf`, `.yaml`, `.toml`, `.go`, `.rs`, `.js`, `.py`, `.ts`, `.php`, `.html`, `.css`, `.sql`, `.lua`, `.sh` 같은 자주 쓰는 텍스트 형식에 문법 강조를 지원합니다. `Dockerfile`, `*.Dockerfile`, `Makefile`, `.env.example`, `.gitignore`, `compose.yaml`, `compose.yml`, `docker-compose.yaml`, `docker-compose.yml`, `nginx.conf`, `*.nginx.conf`도 파일명 기준으로 감지합니다.

19. **긴 한 줄 Wrap 모드**: Paste에 매우 긴 한 줄이 포함되어 있으면 보기 페이지에 `Long line detected` 힌트와 `Wrap` 버튼이 표시되어, 가로 스크롤 대신 줄바꿈된 읽기 모드로 전환할 수 있습니다.

20. **Paste 복제 지원**: 보기 페이지에서 `Clone` 버튼을 눌러 현재 Paste 내용을 새로운 링크로 복제할 수 있습니다.

### 데이터 정책
데이터 정책 헤더에 대한 설명은 [DATA_POLICY_ko.md](./DATA_POLICY_ko.md)를 참고하세요.
