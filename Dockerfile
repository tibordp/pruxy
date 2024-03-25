FROM golang:1.22-alpine as builder
WORKDIR /usr/src/app

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .
RUN go build -o /usr/local/bin/pruxy pruxy.go

FROM alpine:3.12
COPY --from=builder /usr/local/bin/pruxy /usr/local/bin/pruxy
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/pruxy"]


