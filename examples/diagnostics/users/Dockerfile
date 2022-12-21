FROM alpine

RUN adduser -D newuser

USER newuser
WORKDIR /home/newuser

ENTRYPOINT ["whoami"]
