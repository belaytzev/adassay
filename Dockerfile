FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/adassay-server ./cmd/adassay-server
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/adassay ./cmd/adassay
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/adassay-server /adassay-server
COPY --from=build /out/adassay /adassay
COPY --from=build --chown=nonroot:nonroot /out/data /data
COPY deploy/seed/feeds.txt /feeds.txt
ENV ADASSAY_SERVER_DB=/data/adassay-server.db
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/adassay-server"]
