FROM golang:1.26.0

RUN apt-get update && apt-get install -y \
    docker.io 

WORKDIR /api

COPY go.mod .
RUN go mod tidy
COPY . .

RUN go build -o api

CMD ["./api"]