FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/adfilter-server ./cmd/adfilter-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/adfilter-server /adfilter-server
ENV ADFILTER_SERVER_DB=/data/adfilter-server.db
EXPOSE 8080
ENTRYPOINT ["/adfilter-server"]
