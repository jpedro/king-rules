FROM golang:alpine AS build

WORKDIR /srv
COPY . .
RUN GOARCH=amd64 GOOS=linux go build -o king-rules .


FROM golang:alpine AS final
COPY --from=build /srv/king-rules /srv/king-rules
CMD ["/srv/king-rules"]
