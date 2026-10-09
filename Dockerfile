FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/scqos-webhook ./cmd/webhook

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/scqos-webhook /scqos-webhook
EXPOSE 8443
USER 65532:65532
ENTRYPOINT ["/scqos-webhook"]
