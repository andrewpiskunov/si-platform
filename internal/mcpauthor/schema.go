package mcpauthor

import (
	"github.com/google/jsonschema-go/jsonschema"
	"reflect"
	"siplatform/internal/packs"
)

// Parameter is recursive. Infer its fields from the canonical model, replacing
// only the recursive edge with a local JSON Schema reference.
func inputSchema[In any]() *jsonschema.Schema {
	ref := &jsonschema.Schema{Ref: "#/$defs/parameter"}
	parameter, e := jsonschema.For[packs.Parameter](&jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[[]packs.Parameter](): {Type: "array", Items: ref},
	}})
	if e != nil {
		panic(e)
	}
	input, e := jsonschema.For[In](&jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{reflect.TypeFor[packs.Parameter](): ref}})
	if e != nil {
		panic(e)
	}
	input.Defs = map[string]*jsonschema.Schema{"parameter": parameter}
	return input
}
