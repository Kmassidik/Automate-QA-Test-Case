# --- build ---
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
# No external deps (stdlib only); copy go.sum if/when one exists.
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/qa-ai ./cmd/qa-ai

# --- run ---
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/qa-ai /app/qa-ai
EXPOSE 8081
USER nonroot:nonroot
ENTRYPOINT ["/app/qa-ai"]
