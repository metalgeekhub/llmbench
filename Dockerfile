# syntax=docker/dockerfile:1

FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/build/ internal/ui/dist/
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/llmbench ./cmd/llmbench \
    && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/llmbench /llmbench
COPY --from=build --chown=65532:65532 /out/data /data
ENV LLMB_LISTEN=:8080 \
    LLMB_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/llmbench"]
CMD ["serve"]
