# Data Policy - Korean

Pastebox는 업로드된 콘텐츠가 얼마나 오래 저장되고, 어떤 조건에서 삭제되는지를 제어하기 위한 데이터 정책을 지원합니다.

데이터 정책은 `curl`로 업로드할 때 `data-policy` 헤더를 추가하여 선택할 수 있습니다.

## 지원하는 정책

| 정책 | 헤더 |
|------|------|
| 30일 저장 | 없음 |
| 영구 저장 | data-policy: permanent |
| 일회성 저장 | data-policy: once |
| 사용자 지정 만료 | data-policy: 30m / 12h / 7d |

## 30일 저장
헤더 없이 작동하는 기본 정책이며 30일간 파일을 보관 후 자동으로 삭제합니다.

```bash
curl -F "file=@test.txt" http://localhost:8080
```

## 영구 저장
삭제 없이 영구적으로 저장하며 수동으로 삭제하기 전까지 보관됩니다. 업로드시 발급된 `?delete=코드` 파라미터가 붙은 링크로 직접 삭제할 수 있습니다.

```bash
curl -H "data-policy: permanent" -F "file=@test.txt" http://localhost:8080
```

## 일회성 저장
링크가 발급된 이후 첫 성공 조회 후 자동으로 삭제됩니다. 일회성으로 공유해야 하는 내용이나 복사가 불가능한 환경에서 민감한 내용을 공유할 때 사용할 수 있습니다.

```bash
curl -H "data-policy: once" -F "file=@test.txt" http://localhost:8080
```

## 사용자 지정 만료
`data-policy` 헤더에 기간 값을 넣으면 최대 30일 이내로 사용자 지정 만료 시간을 설정할 수 있습니다.

지원 단위:

- `m`: 분
- `h`: 시간
- `d`: 일

예시:

```bash
curl -H "data-policy: 30m" -F "file=@test.txt" http://localhost:8080
curl -H "data-policy: 12h" -F "file=@test.txt" http://localhost:8080
curl -H "data-policy: 7d" -F "file=@test.txt" http://localhost:8080
```

값은 양의 정수여야 하며 30일을 초과할 수 없습니다. `0m`, `31d`, `721h`, `1w`, `1.5h` 같은 값은 `400 Bad Request`로 거절됩니다.
