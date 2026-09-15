// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"bytes"
	"context"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// NewTypedTool builds a Tool whose input schema is derived from T and whose handler receives a decoded T.
func NewTypedTool[T any](name, description string, fn func(ctx context.Context, in T) (string, error)) Tool {
	return NewTypedContentTool(name, description, func(ctx context.Context, in T) ([]ToolContent, error) {
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		return ToolText(out), nil
	})
}

// NewTypedContentTool is NewTypedTool for a tool returning content blocks.
func NewTypedContentTool[T any](name, description string, fn func(ctx context.Context, in T) ([]ToolContent, error)) Tool {
	var zero T
	raw, _ := json.Marshal(jsonSchemaFor(reflect.TypeOf(zero), map[reflect.Type]bool{}))
	return ToolFunc{
		NameVal:        name,
		DescriptionVal: description,
		SchemaVal:      raw,
		Fn: func(ctx context.Context, input json.RawMessage) ([]ToolContent, error) {
			v, err := decodeToolInput[T](name, input)
			if err != nil {
				return nil, err
			}
			return fn(ctx, v)
		},
	}
}

func decodeToolInput[T any](name string, input json.RawMessage) (T, error) {
	var v T
	if len(bytes.TrimSpace(input)) > 0 {
		if err := json.Unmarshal(input, &v); err != nil {
			return v, fmt.Errorf("golm: tool %q input: %w", name, err)
		}
	}
	return v, nil
}

var (
	rawMessageType      = reflect.TypeOf(json.RawMessage(nil))
	textMarshalerType   = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

func marshalsAsString(t reflect.Type) bool {
	pt := reflect.PointerTo(t)
	marshals := t.Implements(textMarshalerType) || pt.Implements(textMarshalerType)
	unmarshals := t.Implements(textUnmarshalerType) || pt.Implements(textUnmarshalerType)
	return marshals && unmarshals
}

func jsonSchemaFor(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == rawMessageType {
		return map[string]any{}
	}
	if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
		return map[string]any{"type": "string"}
	}
	if marshalsAsString(t) {
		return map[string]any{"type": "string"}
	}
	switch t.Kind() {
	case reflect.Struct:
		if seen[t] {
			return map[string]any{}
		}
		seen[t] = true
		defer delete(seen, t)
		props := map[string]any{}
		var required []string
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			rawName, omitempty, skip := jsonTagInfo(f)
			if skip {
				continue
			}
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}

			if f.Anonymous && rawName == "" && ft.Kind() == reflect.Struct && ft != rawMessageType {
				sub := jsonSchemaFor(ft, seen)
				if sp, ok := sub["properties"].(map[string]any); ok {
					for k, v := range sp {
						props[k] = v
					}
				}
				if sr, ok := sub["required"].([]string); ok {
					required = append(required, sr...)
				}
				continue
			}
			if !f.IsExported() {
				continue
			}
			name := rawName
			if name == "" {
				name = f.Name
			}
			props[name] = jsonSchemaFor(f.Type, seen)
			if !omitempty && f.Type.Kind() != reflect.Pointer {
				required = append(required, name)
			}
		}
		m := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			m["required"] = required
		}
		return m
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": jsonSchemaFor(t.Elem(), seen)}
	case reflect.Map:
		return map[string]any{"type": "object"}
	default:
		return map[string]any{}
	}
}

func jsonTagInfo(f reflect.StructField) (rawName string, omitempty, skip bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	parts := strings.Split(tag, ",")
	rawName = parts[0]
	for _, p := range parts[1:] {
		if p == "omitempty" {
			omitempty = true
		}
	}
	return rawName, omitempty, false
}
