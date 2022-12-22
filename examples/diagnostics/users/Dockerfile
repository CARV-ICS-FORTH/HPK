FROM ubuntu

RUN useradd --shell /bin/bash --create-home newuser

USER newuser
WORKDIR /home/newuser

ENTRYPOINT ["whoami"]

