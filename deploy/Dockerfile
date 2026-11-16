FROM scratch
COPY herdr-recall-server /herdr-recall-server
USER 65532:65532
ENV HOME=/data
ENTRYPOINT ["/herdr-recall-server", "--db", "/data/server.db"]
