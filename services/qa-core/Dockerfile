# --- build ---
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
# Templates + static assets are embedded via //go:embed, so the binary is self-contained.
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/qa-core ./cmd/qa-core

# --- run ---
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/qa-core /app/qa-core
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/qa-core"]
