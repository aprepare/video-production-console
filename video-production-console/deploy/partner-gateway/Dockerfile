FROM alpine:3.22
RUN addgroup -g 10001 -S app && adduser -u 10001 -S -G app app
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --chmod=755 --chown=10001:10001 partner-gateway /app/partner-gateway
USER 10001:10001
ENTRYPOINT ["/app/partner-gateway"]
CMD ["serve"]
