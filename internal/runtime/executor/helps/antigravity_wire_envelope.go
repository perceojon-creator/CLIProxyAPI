// Package helps provides helper functions and utilities for provider executors.
package helps

import (
	"bytes"
	"encoding/json"

	"github.com/tidwall/gjson"
)

// nativeTopLevelOrder defines the canonical property sequence emitted by the native
// Antigravity TypeScript/Node.js client.
var nativeTopLevelOrder = []string{
	"project",
	"model",
	"userAgent",
	"requestType",
	"requestId",
}

// nativeInnerRequestOrder defines the property sequence inside the inner "request" object
// emitted by Antigravity Hub.
var nativeInnerRequestOrder = []string{
	"sessionId",
	"contents",
	"tools",
	"toolConfig",
	"systemInstruction",
	"generationConfig",
}

// AlignAntigravityWireEnvelope normalizes and orders the JSON fields of an Antigravity request
// to match the exact serialization sequence and compact whitespace formatting emitted by
// the native V8/Electron client.
func AlignAntigravityWireEnvelope(payload []byte) []byte {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return payload
	}

	root := gjson.ParseBytes(payload)
	if !root.IsObject() {
		return payload
	}

	var buf bytes.Buffer
	buf.Grow(len(payload) + 32)
	buf.WriteByte('{')

	writtenKeys := make(map[string]struct{}, 8)
	first := true

	// 1. Write known top-level fields in native order
	for _, key := range nativeTopLevelOrder {
		res := root.Get(key)
		if res.Exists() {
			if !first {
				buf.WriteByte(',')
			}
			first = false
			keyBytes, _ := json.Marshal(key)
			buf.Write(keyBytes)
			buf.WriteByte(':')
			if res.Type == gjson.String {
				valBytes, _ := json.Marshal(res.String())
				buf.Write(valBytes)
			} else {
				buf.WriteString(res.Raw)
			}
			writtenKeys[key] = struct{}{}
		}
	}

	// 2. Write inner "request" object if present
	reqRes := root.Get("request")
	if reqRes.Exists() {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		buf.WriteString(`"request":`)
		if reqRes.IsObject() {
			buf.WriteByte('{')
			reqWritten := make(map[string]struct{}, 8)
			reqFirst := true

			for _, rk := range nativeInnerRequestOrder {
				rVal := reqRes.Get(rk)
				if rVal.Exists() {
					if !reqFirst {
						buf.WriteByte(',')
					}
					reqFirst = false
					rkBytes, _ := json.Marshal(rk)
					buf.Write(rkBytes)
					buf.WriteByte(':')
					var compVal bytes.Buffer
					if err := json.Compact(&compVal, []byte(rVal.Raw)); err == nil {
						buf.Write(compVal.Bytes())
					} else {
						buf.WriteString(rVal.Raw)
					}
					reqWritten[rk] = struct{}{}
				}
			}

			// Write any additional request properties not in known list
			reqRes.ForEach(func(k, v gjson.Result) bool {
				keyStr := k.String()
				if _, exists := reqWritten[keyStr]; !exists {
					if !reqFirst {
						buf.WriteByte(',')
					}
					reqFirst = false
					rkBytes, _ := json.Marshal(keyStr)
					buf.Write(rkBytes)
					buf.WriteByte(':')
					var compVal bytes.Buffer
					if err := json.Compact(&compVal, []byte(v.Raw)); err == nil {
						buf.Write(compVal.Bytes())
					} else {
						buf.WriteString(v.Raw)
					}
				}
				return true
			})
			buf.WriteByte('}')
		} else {
			buf.WriteString(reqRes.Raw)
		}
		writtenKeys["request"] = struct{}{}
	}

	// 3. Write any additional root properties preserved
	root.ForEach(func(k, v gjson.Result) bool {
		keyStr := k.String()
		if _, exists := writtenKeys[keyStr]; !exists {
			if !first {
				buf.WriteByte(',')
			}
			first = false
			kBytes, _ := json.Marshal(keyStr)
			buf.Write(kBytes)
			buf.WriteByte(':')
			var compVal bytes.Buffer
			if err := json.Compact(&compVal, []byte(v.Raw)); err == nil {
				buf.Write(compVal.Bytes())
			} else {
				buf.WriteString(v.Raw)
			}
		}
		return true
	})

	buf.WriteByte('}')

	var finalComp bytes.Buffer
	if err := json.Compact(&finalComp, buf.Bytes()); err == nil {
		return finalComp.Bytes()
	}
	return buf.Bytes()
}
