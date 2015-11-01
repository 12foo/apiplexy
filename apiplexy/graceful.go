package main

import (
	"net"
	"sync"
	"syscall"
)

var httpWg sync.WaitGroup

type graceConn struct {
	net.Conn
}

func (w graceConn) Close() error {
	httpWg.Done()
	return w.Conn.Close()
}

type graceListener struct {
	net.Listener
	stop    chan error
	stopped bool
}

func (gl *graceListener) Accept() (c net.Conn, err error) {
	c, err = gl.Listener.Accept()
	if err != nil {
		return
	}
	c = graceConn{Conn: c}
	httpWg.Add(1)
	return
}

func (gl *graceListener) Close() error {
	if gl.stopped {
		return syscall.EINVAL
	}
	gl.stop <- nil
	return <-gl.stop
}

func newGraceListener(l net.Listener) (gl *graceListener) {
	gl = &graceListener{Listener: l, stop: make(chan error)}
	go func() {
		_ = <-gl.stop
		gl.stopped = true
		gl.stop <- gl.Listener.Close()
	}()
	return
}
