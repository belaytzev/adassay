FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/adassay-server ./cmd/adassay-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/adassay-server /adassay-server
ENV ADASSAY_SERVER_DB=/data/adassay-server.db
EXPOSE 8080
ENTRYPOINT ["/adassay-server"]
