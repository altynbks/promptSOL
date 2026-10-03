FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /proofpilot ./cmd/proxy

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /proofpilot /proofpilot
EXPOSE 8080
ENTRYPOINT ["/proofpilot"]
