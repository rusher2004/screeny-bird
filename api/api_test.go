package api_test

import (
	"context"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/rusher2004/screeny-bird/api"
)

func TestSpecIsAValidOpenAPIDocument(t *testing.T) {
	t.Parallel()

	doc, err := openapi3.NewLoader().LoadFromData(api.Spec)
	if err != nil {
		t.Fatalf("loading the spec: %v", err)
	}

	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("the spec is not valid OpenAPI: %v", err)
	}

	if doc.Info == nil || doc.Info.Version == "" || doc.Info.Title == "" {
		t.Errorf("info = %+v, want a title and a version", doc.Info)
	}
}

func TestEveryOperationIsNamedAndOnlyReads(t *testing.T) {
	t.Parallel()

	doc, err := openapi3.NewLoader().LoadFromData(api.Spec)
	if err != nil {
		t.Fatalf("loading the spec: %v", err)
	}

	seen := map[string]string{}

	for path, item := range doc.Paths.Map() {
		if item.Post != nil || item.Put != nil || item.Patch != nil || item.Delete != nil {
			t.Errorf("%s declares a write operation; the API is read-only", path)
		}

		if item.Get == nil {
			t.Errorf("%s has no GET operation", path)

			continue
		}

		id := item.Get.OperationID
		if id == "" {
			t.Errorf("GET %s has no operationId, so generated clients cannot name it", path)
		}

		if other, dup := seen[id]; dup {
			t.Errorf("operationId %q is used by both %s and %s", id, other, path)
		}

		seen[id] = path
	}

	if len(seen) != 5 {
		t.Errorf("the spec has %d operations, want 5", len(seen))
	}
}
