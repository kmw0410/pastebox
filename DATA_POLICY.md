# Data Policy - English

Pastebox supports data policies for controlling how long uploaded content is stored and under what conditions it is deleted.

You can select a data policy by adding the `data-policy` header when uploading with `curl`.

## Supported Policies

| Policy | Header |
|--------|--------|
| 30-day storage | None |
| Permanent storage | data-policy: permanent |
| One-time storage | data-policy: once |
| Custom expiration | data-policy: 30m / 12h / 7d |

## 30-Day Storage
This is the default policy that works without a header. Files are kept for 30 days and then automatically deleted.

```bash
curl -F "file=@test.txt" http://localhost:8080
```

## Permanent Storage
Files are stored permanently without automatic deletion and remain available until manually deleted. You can delete them directly using the link with the `?delete=code` parameter issued at upload time.

```bash
curl -H "data-policy: permanent" -F "file=@test.txt" http://localhost:8080
```

## One-Time Storage
After the link is issued, the file is automatically deleted after the first successful view. This can be used for sharing one-time content or sensitive content in environments where copying is not possible.

```bash
curl -H "data-policy: once" -F "file=@test.txt" http://localhost:8080
```

## Custom Expiration
Use a duration value in the `data-policy` header to set a custom expiration up to 30 days.

Supported units:

- `m`: minutes
- `h`: hours
- `d`: days

Examples:

```bash
curl -H "data-policy: 30m" -F "file=@test.txt" http://localhost:8080
curl -H "data-policy: 12h" -F "file=@test.txt" http://localhost:8080
curl -H "data-policy: 7d" -F "file=@test.txt" http://localhost:8080
```

The value must be a positive whole number and must not exceed 30 days. Values such as `0m`, `31d`, `721h`, `1w`, and `1.5h` are rejected with `400 Bad Request`.
