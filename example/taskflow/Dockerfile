# syntax=docker/dockerfile:1
#
# Reference-app image (multi-stage, distroless, non-root, static binary).
#
# In THIS monorepo the taskflow module uses a local `replace` onto the craftgo
# root, so the build context must be the REPO ROOT:
#
#   docker build -f example/taskflow/Dockerfile -t taskflow .
#
# When you copy taskflow out to its own repository: drop the `replace` in
# go.mod, run `go get github.com/craftgodotdev/craftgo@latest`, then simplify
# this file to a single-module build (context `.`, `COPY . .`, build in WORKDIR).

FROM golang:1.26 AS build
WORKDIR /src
COPY . .
WORKDIR /src/example/taskflow
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/taskflow .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/taskflow /app/taskflow
# Ship the documented example config as the container default; override by
# mounting your own over /app/config/config.yaml (see docker-compose.yml).
COPY --from=build /src/example/taskflow/config/example.config.yaml /app/config/config.yaml
EXPOSE 8080 9090
ENTRYPOINT ["/app/taskflow"]
