package tomte

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

type schemaInner struct {
	Label string `json:"label"`
}

type schemaNode struct {
	Value    int          `json:"value"`
	Children []schemaNode `json:"children,omitempty"`
}

type schemaArgs struct {
	schemaInner
	Days    int                `json:"days" jsonschema:"minimum age in days"`
	Note    string             `json:"note,omitempty"`
	Tags    []string           `json:"tags,omitempty"`
	Limits  map[string]float64 `json:"limits,omitempty"`
	When    *time.Time         `json:"when,omitempty"`
	Data    []byte             `json:"data,omitempty"`
	Tree    *schemaNode        `json:"tree,omitempty"`
	Any     any                `json:"any,omitempty"`
	Skipped string             `json:"-"`
	private string
}

func TestSchema(t *testing.T) {
	got, err := json.Marshal(schemaFor(reflect.TypeFor[schemaArgs]()))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"properties":{` +
		`"any":{},` +
		`"data":{"type":"string"},` +
		`"days":{"description":"minimum age in days","type":"integer"},` +
		`"label":{"type":"string"},` +
		`"limits":{"additionalProperties":{"type":"number"},"type":"object"},` +
		`"note":{"type":"string"},` +
		`"tags":{"items":{"type":"string"},"type":"array"},` +
		`"tree":{"properties":{"children":{"items":{},"type":"array"},"value":{"type":"integer"}},"required":["value"],"type":"object"},` +
		`"when":{"format":"date-time","type":"string"}` +
		`},"required":["label","days"],"type":"object"}`
	if string(got) != want {
		t.Errorf("schema:\n got %s\nwant %s", got, want)
	}
}

func TestSchemaEmptyStruct(t *testing.T) {
	got, _ := json.Marshal(schemaFor(reflect.TypeFor[struct{}]()))
	if want := `{"properties":{},"required":[],"type":"object"}`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
