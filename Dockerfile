# Quill runs GoReleaser before this runtime-only image is assembled.
FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 65532 pitpilot \
    && adduser -S -D -H -u 65532 -G pitpilot pitpilot

ARG TARGETARCH
ARG VERSION
ARG COMMIT
COPY dist/pitpilot_linux_${TARGETARCH}_*/pitpilot /usr/local/bin/pitpilot

# Reject stale snapshot artifacts even when the image has a release tag.
RUN if [ -n "${VERSION:-}" ]; then \
      test "$(pitpilot version)" = "pitpilot ${VERSION#v} (${COMMIT})"; \
    fi

USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/pitpilot"]
