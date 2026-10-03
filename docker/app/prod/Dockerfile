FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM alpine:3.22

# ca-certificates: Google/Stripe/YooKassa over HTTPS; tzdata: campaign time zones
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 app

COPY --from=build /out/api /out/worker /usr/local/bin/

USER app
EXPOSE 8080

CMD ["api"]
