FROM alpine:latest

RUN apk --update add nginx

COPY files /usr/share/nginx/html

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
