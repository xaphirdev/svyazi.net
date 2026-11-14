FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go index.html styles.css app.js incident-model.mjs ./
COPY server ./server
COPY data ./data
COPY frontend-assets ./frontend-assets
RUN CGO_ENABLED=0 go build -trimpath -o /out/svyazi.net .

FROM debian:bookworm-slim
WORKDIR /app
COPY --from=build /out/svyazi.net /usr/local/bin/svyazi.net
USER 65532:65532
EXPOSE 8000
CMD ["svyazi.net"]
