# syntax=docker/dockerfile:1

FROM alpine:3.23.4 AS builder

RUN printf '%s\n' \
  'https://mirror5.krfoss.org/alpine/v3.23/main' \
  'https://mirror5.krfoss.org/alpine/v3.23/community' \
  > /etc/apk/repositories \
  && apk update \
  && apk upgrade --no-cache \
  && apk add --no-cache go ca-certificates tzdata

WORKDIR /src

COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

# Generate go.sum inside the Docker build context.
# This fixes builds where go.sum does not exist on the host.
RUN go mod tidy

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/pastebox ./cmd/server

FROM alpine:3.23.4

RUN printf '%s\n' \
  'https://mirror5.krfoss.org/alpine/v3.23/main' \
  'https://mirror5.krfoss.org/alpine/v3.23/community' \
  > /etc/apk/repositories \
  && apk update \
  && apk upgrade --no-cache \
  && apk add --no-cache ca-certificates tzdata su-exec \
  && addgroup -S pastebox \
  && adduser -S -G pastebox pastebox

WORKDIR /app

COPY --from=builder /out/pastebox /usr/local/bin/pastebox
COPY templates ./templates
COPY locales ./locales
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN chmod +x /usr/local/bin/docker-entrypoint.sh

ENV LISTEN_ADDR=:8080
ENV DATA_DIR=/paste-data
ENV EXPIRE_DAYS=30
ENV LANGUAGE=en
ENV TZ=Asia/Seoul

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/local/bin/pastebox"]
