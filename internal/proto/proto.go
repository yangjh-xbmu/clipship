// Package proto defines the wire protocol between clipship daemon and pull
// clients. Requests are a single text line: `GET <kind> [force]\n`.
// Responses start with a header line and are followed by raw bytes.
package proto

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// Request is what a pull client sends to the daemon.
type Request struct {
	Kind  string // "png" | "file" | "auto"
	Force bool   // ignore daemon-side soft size limit
}

// Response describes the kind/shape of a daemon response header.
//
//	Kind "png"  -> body is PNG bytes until EOF
//	Kind "file" -> body is Size bytes, original filename = Name
//	Kind "tar"  -> body is Size bytes, a tar stream
//	Kind "err"  -> no body; Err is the message
type Response struct {
	Kind string
	Name string // set for "file"
	Size int64  // set for "file" / "tar"
	Err  string // set for "err"
}

var validKinds = map[string]bool{"png": true, "file": true, "auto": true}

// WriteRequest serializes req to w as a single line.
func WriteRequest(w io.Writer, req Request) error {
	if !validKinds[req.Kind] {
		return fmt.Errorf("proto: invalid request kind %q", req.Kind)
	}
	line := "GET " + req.Kind
	if req.Force {
		line += " force"
	}
	line += "\n"
	_, err := io.WriteString(w, line)
	return err
}

// ReadRequest parses a single request line from r.
func ReadRequest(r *bufio.Reader) (Request, error) {
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return Request{}, err
	}
	line = strings.TrimRight(line, "\r\n")
	parts := strings.Split(line, " ")
	if len(parts) < 2 || parts[0] != "GET" {
		return Request{}, fmt.Errorf("proto: malformed request %q", line)
	}
	kind := parts[1]
	if !validKinds[kind] {
		return Request{}, fmt.Errorf("proto: unknown kind %q", kind)
	}
	force := false
	switch len(parts) {
	case 2:
	case 3:
		if parts[2] != "force" {
			return Request{}, fmt.Errorf("proto: unknown modifier %q", parts[2])
		}
		force = true
	default:
		return Request{}, fmt.Errorf("proto: too many tokens in %q", line)
	}
	return Request{Kind: kind, Force: force}, nil
}

// WriteHeader writes a single response header line (no body).
func WriteHeader(w io.Writer, resp Response) error {
	switch resp.Kind {
	case "png":
		_, err := io.WriteString(w, "TYPE png\n")
		return err
	case "file":
		enc := url.PathEscape(resp.Name)
		_, err := fmt.Fprintf(w, "TYPE file %s %d\n", enc, resp.Size)
		return err
	case "tar":
		_, err := fmt.Fprintf(w, "TYPE tar %d\n", resp.Size)
		return err
	case "err":
		_, err := fmt.Fprintf(w, "ERR %s\n", resp.Err)
		return err
	default:
		return fmt.Errorf("proto: invalid response kind %q", resp.Kind)
	}
}

// ReadHeader parses a single response header line from r.
func ReadHeader(r *bufio.Reader) (Response, error) {
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return Response{}, err
	}
	line = strings.TrimRight(line, "\r\n")

	if strings.HasPrefix(line, "ERR ") {
		return Response{Kind: "err", Err: strings.TrimPrefix(line, "ERR ")}, nil
	}
	if !strings.HasPrefix(line, "TYPE ") {
		return Response{}, fmt.Errorf("proto: malformed header %q", line)
	}
	rest := strings.TrimPrefix(line, "TYPE ")
	parts := strings.Split(rest, " ")
	switch parts[0] {
	case "png":
		if len(parts) != 1 {
			return Response{}, fmt.Errorf("proto: png header has extra tokens: %q", line)
		}
		return Response{Kind: "png"}, nil
	case "file":
		if len(parts) != 3 {
			return Response{}, fmt.Errorf("proto: file header wants name+size: %q", line)
		}
		name, err := url.PathUnescape(parts[1])
		if err != nil {
			return Response{}, fmt.Errorf("proto: decode name: %w", err)
		}
		size, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return Response{}, fmt.Errorf("proto: parse file size: %w", err)
		}
		return Response{Kind: "file", Name: name, Size: size}, nil
	case "tar":
		if len(parts) != 2 {
			return Response{}, fmt.Errorf("proto: tar header wants size: %q", line)
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return Response{}, fmt.Errorf("proto: parse tar size: %w", err)
		}
		return Response{Kind: "tar", Size: size}, nil
	default:
		return Response{}, fmt.Errorf("proto: unknown TYPE kind %q", parts[0])
	}
}
