FROM alpine

RUN addgroup -S newuser && adduser -S newuser -G newuser

RUN apk add util-linux

USER newuser
WORKDIR /home/newuser

ENTRYPOINT ["whoami"]