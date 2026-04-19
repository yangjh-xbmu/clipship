package proto

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"
)

func TestWriteReadRequest_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Request
		wire string
	}{
		{"png", Request{Kind: "png"}, "GET png\n"},
		{"file", Request{Kind: "file"}, "GET file\n"},
		{"file force", Request{Kind: "file", Force: true}, "GET file force\n"},
		{"auto", Request{Kind: "auto"}, "GET auto\n"},
		{"auto force", Request{Kind: "auto", Force: true}, "GET auto force\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteRequest(&buf, tc.in); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := buf.String(); got != tc.wire {
				t.Fatalf("wire = %q, want %q", got, tc.wire)
			}
			got, err := ReadRequest(bufio.NewReader(strings.NewReader(tc.wire)))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got != tc.in {
				t.Fatalf("round-trip = %+v, want %+v", got, tc.in)
			}
		})
	}
}

func TestReadRequest_Errors(t *testing.T) {
	cases := []struct {
		name string
		wire string
	}{
		{"no GET prefix", "PNG\n"},
		{"unknown kind", "GET zip\n"},
		{"extra garbage", "GET png extra\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadRequest(bufio.NewReader(strings.NewReader(tc.wire)))
			if err == nil {
				t.Fatalf("want error for %q", tc.wire)
			}
		})
	}
}

func TestReadRequest_EOF(t *testing.T) {
	_, err := ReadRequest(bufio.NewReader(strings.NewReader("")))
	if err == nil {
		t.Fatalf("want non-nil error on EOF, got nil")
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestWriteReadHeader_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Response
		wire string
	}{
		{"png", Response{Kind: "png"}, "TYPE png\n"},
		{"file simple", Response{Kind: "file", Name: "report.pdf", Size: 1024}, "TYPE file report.pdf 1024\n"},
		{"file spaces", Response{Kind: "file", Name: "my report.pdf", Size: 10}, "TYPE file my%20report.pdf 10\n"},
		{"file unicode", Response{Kind: "file", Name: "报告.pdf", Size: 0}, "TYPE file %E6%8A%A5%E5%91%8A.pdf 0\n"},
		{"tar", Response{Kind: "tar", Size: 2048}, "TYPE tar 2048\n"},
		{"err", Response{Kind: "err", Err: "clipboard has no files"}, "ERR clipboard has no files\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteHeader(&buf, tc.in); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := buf.String(); got != tc.wire {
				t.Fatalf("wire = %q, want %q", got, tc.wire)
			}
			got, err := ReadHeader(bufio.NewReader(strings.NewReader(tc.wire)))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got != tc.in {
				t.Fatalf("round-trip = %+v, want %+v", got, tc.in)
			}
		})
	}
}

func TestReadHeader_Malformed(t *testing.T) {
	cases := []string{
		"garbage\n",
		"TYPE\n",
		"TYPE zip 10\n",
		"TYPE file missingsize\n",
		"TYPE file badsize abc\n",
		"TYPE tar abc\n",
	}
	for _, w := range cases {
		if _, err := ReadHeader(bufio.NewReader(strings.NewReader(w))); err == nil {
			t.Fatalf("want error for %q", w)
		}
	}
}

func TestEncodeName_PathEscape(t *testing.T) {
	raw := "a b/c:d?e.txt"
	enc := url.PathEscape(raw)
	dec, err := url.PathUnescape(enc)
	if err != nil {
		t.Fatalf("unescape: %v", err)
	}
	if dec != raw {
		t.Fatalf("round-trip mismatch: got %q want %q", dec, raw)
	}
}
