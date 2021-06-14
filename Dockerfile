FROM scratch

COPY king-rules /srv/

ENTRYPOINT [ "/srv/king-rules" ]
