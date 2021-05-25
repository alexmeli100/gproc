FROM golang as builder

ADD . /app

WORKDIR /app/grpc/server/cmd

RUN go mod download

RUN CGO_ENABLED=0 GOOS=linux go build -o /app/server -a -tags netgo -ldflags '-s -w'

FROM ubuntu

RUN apt-get update && apt-get install -y iputils-ping

COPY --from=builder /app/server /app/server
COPY --from=builder /app/certs/localhost.crt /app/server-cert.crt
COPY --from=builder /app/certs/localhost.key /app/server-key.key
COPY --from=builder /app/certs/ca.crt /app/ca.crt
EXPOSE 8083

ENTRYPOINT ["/app/server"]