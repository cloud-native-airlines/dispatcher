FROM golang:1.27 AS build
WORKDIR /src

# Module files first for layer caching.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/dispatcher ./cmd/dispatcher

# Root (not :nonroot): the dispatcher needs access to the mounted Docker socket
# to launch Flight containers.
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/dispatcher /dispatcher
ENV LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["/dispatcher"]
