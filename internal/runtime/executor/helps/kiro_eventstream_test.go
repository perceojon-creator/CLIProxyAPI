package helps

import (
	"encoding/binary"
	"testing"
)

// buildKiroFrame encodes a single AWS event-stream frame with the given headers and payload.
func buildKiroFrame(headers map[string]string, payload []byte) []byte {
	var headerBytes []byte
	for name, value := range headers {
		headerBytes = append(headerBytes, byte(len(name)))
		headerBytes = append(headerBytes, name...)
		headerBytes = append(headerBytes, kiroHeaderTypeStr)
		lenBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBuf, uint16(len(value)))
		headerBytes = append(headerBytes, lenBuf...)
		headerBytes = append(headerBytes, value...)
	}

	total := kiroPreludeBytes + len(headerBytes) + len(payload) + kiroTrailingCRC
	frame := make([]byte, 0, total)
	prelude := make([]byte, kiroPreludeBytes)
	binary.BigEndian.PutUint32(prelude[0:4], uint32(total))
	binary.BigEndian.PutUint32(prelude[4:8], uint32(len(headerBytes)))
	frame = append(frame, prelude...)
	frame = append(frame, headerBytes...)
	frame = append(frame, payload...)
	frame = append(frame, 0, 0, 0, 0)
	return frame
}

func kiroTextFrame(eventType, payload string) []byte {
	return buildKiroFrame(map[string]string{
		":event-type":   eventType,
		":content-type": "application/json",
	}, []byte(payload))
}

func TestKiroEventStreamDecoderDecodesSingleFrame(t *testing.T) {
	decoder := &KiroEventStreamDecoder{}
	events := decoder.Push(kiroTextFrame("assistantResponseEvent", `{"content":"hola"}`))

	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].EventType != "assistantResponseEvent" {
		t.Fatalf("event type = %q, want assistantResponseEvent", events[0].EventType)
	}
	if got := events[0].JSON["content"]; got != "hola" {
		t.Fatalf("content = %v, want hola", got)
	}
}

func TestKiroEventStreamDecoderHandlesSplitFrames(t *testing.T) {
	frame := kiroTextFrame("assistantResponseEvent", `{"content":"split"}`)
	decoder := &KiroEventStreamDecoder{}

	if events := decoder.Push(frame[:7]); len(events) != 0 {
		t.Fatalf("partial prelude produced %d events, want 0", len(events))
	}
	if events := decoder.Push(frame[7 : len(frame)-3]); len(events) != 0 {
		t.Fatalf("partial frame produced %d events, want 0", len(events))
	}
	events := decoder.Push(frame[len(frame)-3:])
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if got := events[0].JSON["content"]; got != "split" {
		t.Fatalf("content = %v, want split", got)
	}
}

func TestKiroEventStreamDecoderDecodesMultipleFramesInOneChunk(t *testing.T) {
	chunk := append(kiroTextFrame("assistantResponseEvent", `{"content":"a"}`),
		kiroTextFrame("metadataEvent", `{"stopReason":"END_TURN"}`)...)

	events := (&KiroEventStreamDecoder{}).Push(chunk)
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[1].EventType != "metadataEvent" {
		t.Fatalf("second event type = %q, want metadataEvent", events[1].EventType)
	}
}

func TestKiroEventStreamDecoderDropsMalformedPrelude(t *testing.T) {
	malformed := make([]byte, kiroPreludeBytes)
	binary.BigEndian.PutUint32(malformed[0:4], 4) // total shorter than the prelude
	decoder := &KiroEventStreamDecoder{}

	if events := decoder.Push(malformed); len(events) != 0 {
		t.Fatalf("events = %d, want 0", len(events))
	}
	if decoder.buf != nil {
		t.Fatal("buffer should be dropped after a malformed prelude")
	}
}

func TestKiroEventStreamDecoderSkipsNonStringHeaders(t *testing.T) {
	// Header type 4 (int32) must be skipped by its fixed width without corrupting the payload.
	headerBytes := []byte{byte(len(":m")), ':', 'm', 4, 0, 0, 0, 1}
	nameEvent := ":event-type"
	headerBytes = append(headerBytes, byte(len(nameEvent)))
	headerBytes = append(headerBytes, nameEvent...)
	headerBytes = append(headerBytes, kiroHeaderTypeStr, 0, byte(len("meteringEvent")))
	headerBytes = append(headerBytes, "meteringEvent"...)
	nameContent := ":content-type"
	headerBytes = append(headerBytes, byte(len(nameContent)))
	headerBytes = append(headerBytes, nameContent...)
	headerBytes = append(headerBytes, kiroHeaderTypeStr, 0, byte(len("application/json")))
	headerBytes = append(headerBytes, "application/json"...)

	payload := []byte(`{"usage":3}`)
	total := kiroPreludeBytes + len(headerBytes) + len(payload) + kiroTrailingCRC
	frame := make([]byte, kiroPreludeBytes)
	binary.BigEndian.PutUint32(frame[0:4], uint32(total))
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(headerBytes)))
	frame = append(frame, headerBytes...)
	frame = append(frame, payload...)
	frame = append(frame, 0, 0, 0, 0)

	events := (&KiroEventStreamDecoder{}).Push(frame)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].EventType != "meteringEvent" {
		t.Fatalf("event type = %q, want meteringEvent", events[0].EventType)
	}
	if got, ok := events[0].JSON["usage"].(float64); !ok || got != 3 {
		t.Fatalf("usage = %v, want 3", events[0].JSON["usage"])
	}
}
