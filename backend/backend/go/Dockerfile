FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build -o /reservation .

FROM alpine:3.20
COPY --from=build /reservation /usr/local/bin/reservation
EXPOSE 8080
ENTRYPOINT ["reservation"]
