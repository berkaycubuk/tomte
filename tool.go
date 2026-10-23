package tomte

// Tools are Go functions the model can call. The model sends arguments as
// JSON, which are decoded into the function's input type; the result is
// sent back as JSON, or as-is when it's a string. An error or a panic is
// sent back too, so the model can try something else.

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
)

// maxResult caps what one tool call adds to the conversation, so a tool
// that returns a whole file doesn't fill the context or the bill.
const maxResult = 32 << 10

type tool struct {
	spec toolSpec
	call func(ctx context.Context, args string) string
}

// Tool lets the model call fn. In must be a struct; its fields are the
// tool's arguments (see schema.go for the tags).
func Tool[In, Out any](name, description string, fn func(context.Context, In) (Out, error)) Option {
	in := reflect.TypeFor[In]()
	if in.Kind() != reflect.Struct {
		panic(fmt.Sprintf("tomte: tool %s: input must be a struct, not %s", name, in))
	}
	params, err := json.Marshal(schemaFor(in))
	if err != nil {
		panic(fmt.Sprintf("tomte: tool %s: %v", name, err))
	}
	t := &tool{}
	t.spec.Type = "function"
	t.spec.Function.Name = name
	t.spec.Function.Description = description
	t.spec.Function.Parameters = params
	t.call = func(ctx context.Context, args string) (result string) {
		defer func() {
			if r := recover(); r != nil {
				result = fmt.Sprintf("error: tool panicked: %v", r)
			}
		}()
		var v In
		if args == "" {
			args = "{}"
		}
		if err := json.Unmarshal([]byte(args), &v); err != nil {
			return "error: invalid arguments: " + err.Error()
		}
		out, err := fn(ctx, v)
		if err != nil {
			return "error: " + err.Error()
		}
		if s, ok := any(out).(string); ok {
			return truncate(s)
		}
		b, err := json.Marshal(out)
		if err != nil {
			return "error: encoding the result: " + err.Error()
		}
		return truncate(string(b))
	}
	return func(a *Agent) {
		if _, dup := a.tools[name]; dup {
			a.errs = append(a.errs, fmt.Errorf("tool %s is declared twice", name))
			return
		}
		a.tools[name] = t
		a.specs = append(a.specs, t.spec)
	}
}

func truncate(s string) string {
	if len(s) <= maxResult {
		return s
	}
	return s[:maxResult] + fmt.Sprintf("\n[cut off: %d more bytes]", len(s)-maxResult)
}
