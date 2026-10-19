# Build context is backend/ so the shared platform module is available:
#   docker build -f backend/document-service/Dockerfile backend
FROM golang:1.27-alpine AS build
ENV GOWORK=off CGO_ENABLED=0
WORKDIR /src

# Dependencies first, so they are cached until a go.mod or go.sum changes.
COPY platform/go.mod platform/go.sum ./platform/
COPY document-service/go.mod document-service/go.sum* ./document-service/
RUN cd document-service && go mod download

COPY platform ./platform
COPY document-service ./document-service
RUN cd document-service && go build -o /out/service .

FROM alpine:3.21
COPY --from=build /out/service /service
EXPOSE 8080
CMD ["/service"]
