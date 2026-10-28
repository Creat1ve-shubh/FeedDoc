# Builds the Shopify app server into a small static image.
#   docker build -t feeddoctor .
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/feeddoctor-server ./cmd/feeddoctor-server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/feeddoctor ./cmd/feeddoctor

# distroless: no shell or package manager, runs as a non-root user, and
# carries the CA certificates needed to call Shopify over https.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /usr/local/bin/
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/feeddoctor-server"]
