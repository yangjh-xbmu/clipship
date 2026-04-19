package server

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/yangjh-xbmu/clipship/internal/clipboard"
)

// Protocol (intentionally tiny):
//   client connects, server writes either
//     - raw PNG bytes + close, or
//     - "ERR <message>\n" (prefix never collides with PNG magic)
//   client reads until EOF.

func Run(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	defer ln.Close()
	fmt.Printf("clipship daemon listening on %s\n", addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return err
		}
		go handle(conn)
	}
}

func handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	img, err := clipboard.ReadPNG()
	if err != nil {
		fmt.Fprintf(conn, "ERR %s\n", err.Error())
		return
	}
	if _, err := io.Copy(conn, bytes.NewReader(img)); err != nil {
		fmt.Fprintf(conn, "ERR write: %s\n", err.Error())
	}
}
