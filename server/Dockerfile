FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN go build -o /out/backroute-server .

FROM alpine:3.20

WORKDIR /app
COPY --from=build /out/backroute-server /app/backroute-server
EXPOSE 8080
ENTRYPOINT ["/app/backroute-server"]
