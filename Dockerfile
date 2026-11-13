FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -o /server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /server /server
COPY index.html i18n-zh.js ./
COPY dist ./dist
COPY client ./client
COPY assets ./assets
EXPOSE 8443/tcp 8443/udp
ENTRYPOINT ["/server"]
CMD ["-http-addr="]
