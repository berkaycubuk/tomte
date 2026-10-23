package tomte

// JSON schemas for tool arguments, made from the Go type the tool takes.
// Fields are named by their json tag, a field without omitempty is
// required, and the jsonschema tag is the field's description:
//
//	type args struct {
//		Days int    `json:"days" jsonschema:"minimum age in days"`
//		Note string `json:"note,omitempty"`
//	}

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

var (
	timeType = reflect.TypeFor[time.Time]()
	rawType  = reflect.TypeFor[json.RawMessage]()
)

type schema map[string]any

func schemaFor(t reflect.Type) schema {
	return schemaOf(t, map[reflect.Type]bool{})
}

// seen holds the struct types being described, so a type that contains
// itself becomes an empty schema (anything) instead of recursing forever.
func schemaOf(t reflect.Type, seen map[reflect.Type]bool) schema {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == timeType:
		return schema{"type": "string", "format": "date-time"}
	case t == rawType:
		return schema{}
	}
	switch t.Kind() {
	case reflect.String:
		return schema{"type": "string"}
	case reflect.Bool:
		return schema{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return schema{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return schema{"type": "number"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return schema{"type": "string"} // []byte is base64 in JSON
		}
		return schema{"type": "array", "items": schemaOf(t.Elem(), seen)}
	case reflect.Map:
		return schema{"type": "object", "additionalProperties": schemaOf(t.Elem(), seen)}
	case reflect.Struct:
		if seen[t] {
			return schema{}
		}
		seen[t] = true
		defer delete(seen, t)
		props := schema{}
		required := []string{}
		addFields(t, props, &required, seen)
		return schema{"type": "object", "properties": props, "required": required}
	}
	return schema{} // interfaces and anything else: any value
}

func addFields(t reflect.Type, props schema, required *[]string, seen map[reflect.Type]bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		// Embedded structs without a name are flattened, as encoding/json does.
		if f.Anonymous && name == "" && ft.Kind() == reflect.Struct {
			addFields(ft, props, required, seen)
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		s := schemaOf(f.Type, seen)
		if d := f.Tag.Get("jsonschema"); d != "" {
			s["description"] = d
		}
		props[name] = s
		if !strings.Contains(opts, "omitempty") && !strings.Contains(opts, "omitzero") {
			*required = append(*required, name)
		}
	}
}
