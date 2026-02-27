FROM alpine:3.23

WORKDIR /app

RUN apk add --no-cache tzdata

COPY ./build/iptv-scraper /usr/local/bin/

CMD ["iptv-scraper"]
