package vaultproc

import (
	"bytes"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	sent := request{V: Version, Op: opSet, Credential: "wiki-reader", Role: "token", Value: "synthetic-token"}
	done := make(chan error, 1)
	go func() { done <- writeMessage(a, sent) }()

	var got request
	if err := readMessage(b, &got); err != nil {
		t.Fatalf("readMessage() error = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("writeMessage() error = %v", err)
	}
	if !reflect.DeepEqual(got, sent) {
		t.Fatalf("readMessage() = %+v, want %+v", got, sent)
	}
}

func TestReadMessageBoundsItsInput(t *testing.T) {
	// A line of exactly MaxMessage bytes, newline included, is still read.
	fits := `{"v":1,"op":"get","credential":"` + strings.Repeat("a", MaxMessage) + `"}`
	fits = fits[:MaxMessage-3] + `"}` + "\n"
	var req request
	if err := readMessage(strings.NewReader(fits), &req); err != nil {
		t.Fatalf("readMessage() of %d bytes error = %v", len(fits), err)
	}

	// One byte more, or a line that never ends, is refused without reading further.
	source := &countingReader{r: strings.NewReader(strings.Repeat("x", 4*MaxMessage))}
	if err := readMessage(source, &req); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("readMessage() of an endless line error = %v, want ErrTooLarge", err)
	}
	if source.n > MaxMessage+1 {
		t.Fatalf("readMessage() read %d bytes, want at most %d", source.n, MaxMessage+1)
	}

	if err := readMessage(strings.NewReader(`{"v":1`), &req); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("readMessage() of a cut-off line error = %v, want io.ErrUnexpectedEOF", err)
	}
	if err := readMessage(strings.NewReader(""), &req); !errors.Is(err, io.EOF) {
		t.Fatalf("readMessage() of nothing error = %v, want io.EOF", err)
	}
}

func TestReadMessageNeverQuotesItsInput(t *testing.T) {
	var req request
	err := readMessage(strings.NewReader("synthetic-secret-value\n"), &req)
	if err == nil || strings.Contains(err.Error(), "synthetic-secret-value") {
		t.Fatalf("readMessage() of invalid JSON error = %v, want an error without the input", err)
	}
}

func TestWriteMessageRefusesTooLarge(t *testing.T) {
	var out bytes.Buffer
	err := writeMessage(&out, request{V: Version, Op: opSet, Credential: "c", Role: "r",
		Value: strings.Repeat("x", MaxMessage)})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("writeMessage() error = %v, want ErrTooLarge", err)
	}
	if out.Len() != 0 {
		t.Fatalf("writeMessage() wrote %d bytes of a message it refused", out.Len())
	}
}

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}
