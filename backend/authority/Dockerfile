FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -o /authority .

FROM alpine:3.20
COPY --from=build /authority /usr/local/bin/authority
EXPOSE 9000
ENTRYPOINT ["authority"]
