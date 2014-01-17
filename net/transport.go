package net

import (
	"errors"
)

var (
	ErrTransportClosed = errors.New("transport closed")
)

type Transport interface {
	// Network returns the name of the netwpath type
	Network() string

	// Open opens the connection
	Open() error

	// ReadFrom reads a packet from the connection,
	// copying the payload into b.  It returns the number of
	// bytes copied into b and the return address that
	// was on the packet.
	ReadFrom(b []byte) (n int, addr Addr, err error)

	// WriteTo writes a packet with payload b to addr.
	WriteTo(b []byte, addr Addr) (n int, err error)

	// Close closes the connection.
	// Any blocked ReadFrom or WriteTo operations will be unblocked and return errors.
	Close() error

	// LocalAddresses returns any local addresses.
	LocalAddresses() []Addr

	// parse a json encoded `path` object.
	DecodeAddr(data []byte) (Addr, error)

	// format a json encoded `path` object.
	EncodeAddr(addr Addr) ([]byte, error)

	FormatSeekAddress(addr Addr) string
	ParseSeekAddress(fields []string) (addr Addr, ok bool)
}