FROM alpine:3.17
ENTRYPOINT ["/vignet"]
STOPSIGNAL SIGINT
COPY vignet /vignet
