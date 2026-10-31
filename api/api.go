// Package api holds the OpenAPI description of the HTTP API the server
// serves. It is the contract the screensaver clients are written against.
package api

import _ "embed"

// Spec is the OpenAPI 3.0 document, in YAML.
//
//go:embed openapi.yaml
var Spec []byte
