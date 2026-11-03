package evidence

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

// Redaction must not turn a short numeric password into an invalid timestamp
// or archive ID. Only typed time.Time fields and the recorder's own summary
// are restored; free-form logs, errors and configuration strings stay scrubbed.
func preserveMetadata(data []byte, original any) []byte {
	var tree any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if decoder.Decode(&tree) != nil {
		return []byte(`{"error":"invalid evidence JSON"}`)
	}
	var restore func(any, reflect.Value) any
	restore = func(dst any, src reflect.Value) any {
		if src.Type() == reflect.TypeOf(time.Time{}) {
			return src.Interface()
		}
		switch src.Kind() {
		case reflect.Struct:
			if object, ok := dst.(map[string]any); ok {
				for i := 0; i < src.NumField(); i++ {
					field := src.Type().Field(i)
					if !field.IsExported() {
						continue
					}
					if field.Anonymous {
						restore(object, src.Field(i))
						continue
					}
					name := strings.Split(field.Tag.Get("json"), ",")[0]
					if name == "" {
						name = field.Name
					}
					if child, exists := object[name]; exists && name != "-" {
						object[name] = restore(child, src.Field(i))
					}
				}
			}
		case reflect.Slice, reflect.Array:
			if array, ok := dst.([]any); ok {
				for i := 0; i < len(array) && i < src.Len(); i++ {
					array[i] = restore(array[i], src.Index(i))
				}
			}
		case reflect.Map:
			if object, ok := dst.(map[string]any); ok && src.Type().Key().Kind() == reflect.String {
				iter := src.MapRange()
				for iter.Next() {
					key := iter.Key().String()
					if child, exists := object[key]; exists {
						object[key] = restore(child, iter.Value())
					}
				}
			}
		}
		return dst
	}
	tree = restore(tree, reflect.ValueOf(original))
	if report, ok := original.(Report); ok {
		b, _ := json.Marshal(report.Summary)
		var summary map[string]any
		_ = json.Unmarshal(b, &summary)
		if object, ok := tree.(map[string]any); ok {
			for k, v := range summary {
				object[k] = v
			}
		}
	}
	b, _ := json.Marshal(tree)
	return b
}
