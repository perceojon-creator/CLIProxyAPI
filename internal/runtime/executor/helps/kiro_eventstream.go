package helps

import (
	"encoding/binary"
	"encoding/json"
	"strings"
)

// KiroEvent is a decoded frame from the AWS event-stream response.
type KiroEvent struct {
	// EventType is the ":event-type" header value (e.g. "assistantResponseEvent").
	EventType string
	// JSON is the decoded payload when the frame carries a JSON content type.
	JSON map[string]any
}

// KiroEventStreamDecoder incrementally decodes AWS event-stream frames.
//
// Frame layout (all big-endian):
//
//	[4B total][4B headers_len][4B crc_prelude][headers...][payload...][4B crc_tail]
//
// Each header: [1B name_len][name][1B type][value]
// Type 7 (string): [2B value_len][value_bytes]
type KiroEventStreamDecoder struct {
	buf []byte
}

const (
	kiroPreludeBytes  = 12
	kiroTrailingCRC   = 4
	kiroHeaderTypeStr = 7
)

// kiroHeaderWidths maps fixed-width event-stream header types to their byte widths.
var kiroHeaderWidths = map[byte]int{0: 0, 1: 0, 2: 1, 3: 2, 4: 4, 5: 8, 8: 8}

// Push feeds new bytes into the decoder and returns any complete events extracted.
func (d *KiroEventStreamDecoder) Push(chunk []byte) []KiroEvent {
	if len(chunk) > 0 {
		d.buf = append(d.buf, chunk...)
	}

	var events []KiroEvent
	offset := 0
	for len(d.buf)-offset >= kiroPreludeBytes {
		totalLen := int(binary.BigEndian.Uint32(d.buf[offset : offset+4]))
		headersLen := int(binary.BigEndian.Uint32(d.buf[offset+4 : offset+8]))

		// Malformed prelude — drop the buffer to avoid an infinite loop.
		if totalLen < kiroPreludeBytes+kiroTrailingCRC || headersLen > totalLen {
			d.buf = nil
			return events
		}
		if len(d.buf)-offset < totalLen {
			break
		}

		frame := d.buf[offset : offset+totalLen]
		if evt, ok := decodeKiroFrame(frame, headersLen); ok {
			events = append(events, evt)
		}
		offset += totalLen
	}

	if offset > 0 {
		d.buf = d.buf[offset:]
	}
	return events
}

// decodeKiroFrame extracts headers and payload from a single event-stream frame.
func decodeKiroFrame(frame []byte, headersLen int) (KiroEvent, bool) {
	headers := make(map[string]string)
	cursor := kiroPreludeBytes
	headersEnd := kiroPreludeBytes + headersLen

	for cursor < headersEnd && cursor < len(frame) {
		nameLen := int(frame[cursor])
		cursor++
		if cursor+nameLen > len(frame) {
			break
		}
		name := string(frame[cursor : cursor+nameLen])
		cursor += nameLen

		if cursor >= len(frame) {
			break
		}
		valueType := frame[cursor]
		cursor++

		if valueType == kiroHeaderTypeStr {
			if cursor+2 > len(frame) {
				break
			}
			valueLen := int(binary.BigEndian.Uint16(frame[cursor : cursor+2]))
			cursor += 2
			if cursor+valueLen > len(frame) {
				break
			}
			headers[name] = string(frame[cursor : cursor+valueLen])
			cursor += valueLen
			continue
		}

		// Skip non-string header types by their known fixed widths.
		width, ok := kiroHeaderWidths[valueType]
		if !ok {
			// Unknown type: the width cannot be determined, so skip to the end of headers.
			cursor = headersEnd
			break
		}
		cursor += width
	}

	payloadStart := headersEnd
	payloadEnd := len(frame) - kiroTrailingCRC
	if payloadStart > payloadEnd || payloadEnd > len(frame) {
		return KiroEvent{}, false
	}
	payload := frame[payloadStart:payloadEnd]

	evt := KiroEvent{EventType: headers[":event-type"]}
	if strings.Contains(headers[":content-type"], "json") && len(payload) > 0 {
		var m map[string]any
		if err := json.Unmarshal(payload, &m); err == nil {
			evt.JSON = m
		}
	}
	return evt, true
}
