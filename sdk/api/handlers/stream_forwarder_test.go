package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
)

func TestPendingStreamErrorReturnsBufferedError(t *testing.T) {
	errs := make(chan *interfaces.ErrorMessage, 1)
	want := &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: errors.New("upstream failed")}
	errs <- want
	close(errs)

	got, ok := PendingStreamError(errs)
	if !ok || got != want {
		t.Fatalf("PendingStreamError() = (%#v, %t), want (%#v, true)", got, ok, want)
	}
}

func TestValidateSSEDataJSONAllowsMultilinePayload(t *testing.T) {
	chunk := []byte("event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\n" +
		"data: \"response\":{\"status\":\"completed\"}}\n\n")
	if err := validateSSEDataJSON(chunk); err != nil {
		t.Fatalf("validateSSEDataJSON() error = %v, want nil", err)
	}
}

func TestForwardStreamNormalizesErrorBeforeWriteAndCancel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	data := make(chan []byte)
	close(data)
	errs := make(chan *interfaces.ErrorMessage, 1)
	errs <- &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: errors.New("raw secret")}
	close(errs)

	var written, canceled string
	disabledKeepAlive := time.Duration(0)
	h := &BaseAPIHandler{}
	h.ForwardStream(c, recorder, func(err error) {
		if err != nil {
			canceled = err.Error()
		}
	}, data, errs, StreamForwardOptions{
		KeepAliveInterval: &disabledKeepAlive,
		NormalizeTerminalError: func(errMsg *interfaces.ErrorMessage) *interfaces.ErrorMessage {
			return &interfaces.ErrorMessage{StatusCode: errMsg.StatusCode, Error: errors.New("safe error")}
		},
		WriteTerminalError: func(errMsg *interfaces.ErrorMessage) {
			written = errMsg.Error.Error()
		},
	})

	if written != "safe error" || canceled != "safe error" {
		t.Fatalf("written=%q canceled=%q, want sanitized error", written, canceled)
	}
}

func TestPendingStreamErrorIgnoresUnavailableErrors(t *testing.T) {
	closed := make(chan *interfaces.ErrorMessage)
	close(closed)

	for name, errs := range map[string]<-chan *interfaces.ErrorMessage{
		"nil":          nil,
		"closed empty": closed,
		"open empty":   make(chan *interfaces.ErrorMessage),
	} {
		t.Run(name, func(t *testing.T) {
			if got, ok := PendingStreamError(errs); ok || got != nil {
				t.Fatalf("PendingStreamError() = (%#v, %t), want (nil, false)", got, ok)
			}
		})
	}
}

func TestDefaultSSEByteSlices(t *testing.T) {
	if string(DefaultSSEKeepAliveBytes) != ": keep-alive\n\n" {
		t.Fatalf("unexpected keep-alive bytes: %q", string(DefaultSSEKeepAliveBytes))
	}
	if string(DefaultSSEDoneBytes) != "data: [DONE]\n\n" {
		t.Fatalf("unexpected done bytes: %q", string(DefaultSSEDoneBytes))
	}
	if string(DefaultSSENewlineBytes) != "\n" {
		t.Fatalf("unexpected newline bytes: %q", string(DefaultSSENewlineBytes))
	}
	if string(DefaultSSEDoubleNewlineBytes) != "\n\n" {
		t.Fatalf("unexpected double newline bytes: %q", string(DefaultSSEDoubleNewlineBytes))
	}
}

func TestForwardStream_NilPointersAndEdgeCases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &BaseAPIHandler{}

	t.Run("NilContextAndNilCancel", func(t *testing.T) {
		// Should safely return without panic
		h.ForwardStream(nil, nil, nil, nil, nil, StreamForwardOptions{})
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		h.ForwardStream(c, nil, nil, nil, nil, StreamForwardOptions{})
	})

	t.Run("NilFlusherWithDataAndDone", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

		data := make(chan []byte, 2)
		data <- []byte("data: hello\n\n")
		close(data)

		disabledKeepAlive := time.Duration(0)
		var canceled bool
		h.ForwardStream(c, nil, func(err error) {
			canceled = true
			if err != nil {
				t.Fatalf("unexpected cancel error: %v", err)
			}
		}, data, nil, StreamForwardOptions{
			KeepAliveInterval: &disabledKeepAlive,
			WriteChunk: func(chunk []byte) {
				_, _ = c.Writer.Write(chunk)
			},
			WriteDone: func() {
				_, _ = c.Writer.Write(DefaultSSEDoneBytes)
			},
		})

		if !canceled {
			t.Fatal("expected cancel callback to be invoked")
		}
		got := recorder.Body.String()
		want := "data: hello\n\ndata: [DONE]\n\n"
		if got != want {
			t.Fatalf("body = %q, want %q", got, want)
		}
	})

	t.Run("NilRequestInGinContext", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		// c.Request is nil
		data := make(chan []byte)
		close(data)

		disabledKeepAlive := time.Duration(0)
		var canceled bool
		h.ForwardStream(c, nil, func(err error) {
			canceled = true
		}, data, nil, StreamForwardOptions{
			KeepAliveInterval: &disabledKeepAlive,
		})

		if !canceled {
			t.Fatal("expected cancel to be called even with nil c.Request")
		}
	})

	t.Run("ZeroLengthStreamImmediateClose", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

		data := make(chan []byte)
		close(data)

		disabledKeepAlive := time.Duration(0)
		var canceled bool
		h.ForwardStream(c, nil, func(err error) {
			canceled = true
		}, data, nil, StreamForwardOptions{
			KeepAliveInterval: &disabledKeepAlive,
			WriteDone: func() {
				_, _ = c.Writer.Write(DefaultSSEDoneBytes)
			},
		})

		if !canceled {
			t.Fatal("expected cancel callback to be invoked")
		}
		if got := recorder.Body.String(); got != "data: [DONE]\n\n" {
			t.Fatalf("expected done bytes on zero-length stream, got %q", got)
		}
	})
}

type noopFlusherWriter struct {
	*httptest.ResponseRecorder
}

func (n *noopFlusherWriter) Flush() {}

func BenchmarkForwardStream(b *testing.B) {
	gin.SetMode(gin.ReleaseMode)
	chunkData := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"token\"}}]}\n\n")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

		data := make(chan []byte, 5)
		data <- chunkData
		data <- chunkData
		data <- chunkData
		close(data)

		disabledKeepAlive := time.Duration(0)
		h := &BaseAPIHandler{}
		h.ForwardStream(c, &noopFlusherWriter{recorder}, func(error) {}, data, nil, StreamForwardOptions{
			KeepAliveInterval: &disabledKeepAlive,
			WriteChunk: func(chunk []byte) {
				_, _ = c.Writer.Write(chunk)
			},
			WriteDone: func() {
				_, _ = c.Writer.Write(DefaultSSEDoneBytes)
			},
		})
	}
}
