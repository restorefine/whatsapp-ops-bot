# syntax=docker/dockerfile:1

FROM node:24-alpine AS portal-build
WORKDIR /portal
COPY portal/package.json portal/package-lock.json ./
RUN npm ci
COPY portal/ ./
RUN npm run build

FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/approvals-setup ./cmd/approvals-setup

# distroless/static ships CA certificates, tzdata and a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
ENV APPROVALS_STATIC_DIR=/app/portal
COPY --from=build /out/server /app/server
COPY --from=build /out/approvals-setup /app/approvals-setup
COPY --from=portal-build /portal/out /app/portal
USER nonroot:nonroot
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/app/server", "-healthcheck"]
ENTRYPOINT ["/app/server"]
