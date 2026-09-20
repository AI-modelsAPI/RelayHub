package proxy

import (
	"context"
	"net"
	"testing"
)

func TestLocalListener(t *testing.T) {
	s, e := NewHTTP(HTTPConfig{Addr: "127.0.0.1:0"})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Shutdown(context.Background())
	if s.Addr() == nil {
		t.Fatal("nil addr")
	}
	if _, e := net.ResolveTCPAddr("tcp", s.Addr().String()); e != nil {
		t.Fatal(e)
	}
}
