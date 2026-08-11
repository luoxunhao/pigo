#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config api/v1/oapi-codegen.yaml api/v1/openapi.yaml
go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config api/v1/oapi-codegen-client.yaml api/v1/openapi.yaml
