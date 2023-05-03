FROM alpine

RUN adduser --disabled-password --gecos "" -s /bin/sh newuser

RUN apk add util-linux

USER newuser
WORKDIR /home/newuser

ENTRYPOINT ["whoami"]
