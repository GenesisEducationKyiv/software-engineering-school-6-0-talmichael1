FROM mcr.microsoft.com/playwright:v1.49.1-jammy

COPY --from=golang:1.25 /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}" \
    GOPATH=/go \
    GOCACHE=/go/cache
RUN mkdir -p /go && chmod -R 777 /go

WORKDIR /src