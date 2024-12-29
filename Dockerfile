FROM golang:1.23.4-alpine3.21 AS builder

WORKDIR /app

COPY go.mod go.sum ./ 

RUN go mod download
RUN go mod verify

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o pgbackup

FROM postgres:17-alpine

WORKDIR /app

COPY --from=builder /app/pgbackup .

ENTRYPOINT []

CMD ["./pgbackup"]
