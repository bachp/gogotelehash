package mux

import (
	"bytes"
	"log"
	"testing"

	"github.com/telehash/gogotelehash/Godeps/_workspace/src/github.com/stretchr/testify/assert"

	"github.com/telehash/gogotelehash/transports"
	"github.com/telehash/gogotelehash/transports/udp"
)

func TestManagerWithoutTransports(t *testing.T) {
	assert := assert.New(t)

	var (
		c   = Config{}
		tr  transports.Transport
		err error
	)

	tr, err = c.Open()
	if assert.NoError(err) && assert.NotNil(tr) {

		err = tr.Close()
		assert.NoError(err)
	}
}

func TestManagerWithOneTransport(t *testing.T) {
	assert := assert.New(t)

	var (
		c   = Config{udp.Config{}}
		tr  transports.Transport
		err error
	)

	tr, err = c.Open()
	if assert.NoError(err) && assert.NotNil(tr) {
		t.Logf("addrs=%v", tr.LocalAddresses())

		err = tr.Close()
		assert.NoError(err)
	}
}

func TestManagerDeliverReceive(t *testing.T) {
	assert := assert.New(t)

	var (
		ca  = Config{udp.Config{}}
		cb  = Config{udp.Config{Addr: "127.0.0.1:0"}}
		ta  transports.Transport
		tb  transports.Transport
		err error
	)

	ta, err = ca.Open()
	if assert.NoError(err) && assert.NotNil(ta) {

		tb, err = cb.Open()
		if assert.NoError(err) && assert.NotNil(tb) {

			t.Logf("ta addrs=%v", ta.LocalAddresses())
			t.Logf("tb addrs=%v", tb.LocalAddresses())

			addr := tb.LocalAddresses()[0]
			log.Printf("SND %q to %s", "Hello World!", addr)
			err = ta.WriteMessage([]byte("Hello World!"), addr)
			assert.NoError(err)

			buf := make([]byte, 128)
			n, src, err := tb.ReadMessage(buf)
			if assert.NoError(err) && assert.NotNil(src) {
				assert.Equal("Hello World!", string(buf[:n]))
			}

			err = ta.Close()
			assert.NoError(err)

			err = tb.Close()
			assert.NoError(err)
		}
	}
}

func Benchmark(b *testing.B) {
	A, err := Config{udp.Config{}}.Open()
	if err != nil {
		b.Fatal(err)
	}
	defer A.Close()

	B, err := Config{udp.Config{}}.Open()
	if err != nil {
		b.Fatal(err)
	}
	defer B.Close()

	var (
		msg = []byte("hello")
		dst = B.LocalAddresses()[0]
		out = make([]byte, 100)
	)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err = A.WriteMessage(msg, dst)
		if err != nil {
			b.Fatal(err)
		}

		n, _, err := B.ReadMessage(out)
		if err != nil {
			b.Fatal(err)
		}

		if !bytes.Equal(out[:n], msg) {
			b.Fatalf("invalid message")
		}
	}
}
