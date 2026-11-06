FROM golang:1.26.0

RUN apt-get update && apt-get install -y g++ \
    cppcheck \
    valgrind \
    golang-go 

RUN go install honnef.co/go/tools/cmd/staticcheck@latest

WORKDIR /app

COPY go.mod .
RUN go mod tidy
COPY . .

RUN go build -o checker

CMD ["tail", "-f", "/dev/null"]
#!!!