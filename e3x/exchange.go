package e3x

import (
	"encoding/binary"
	"math/rand"
	"time"

	"bitbucket.org/simonmenke/go-telehash/e3x/cipherset"
	"bitbucket.org/simonmenke/go-telehash/hashname"
	"bitbucket.org/simonmenke/go-telehash/lob"
	"bitbucket.org/simonmenke/go-telehash/transports"
	"bitbucket.org/simonmenke/go-telehash/util/scheduler"
)

type BrokenExchange hashname.H

func (err BrokenExchange) Error() string {
	return "e3x: broken exchange " + string(err)
}

type exchangeState uint8

const (
	unknownExchangeState exchangeState = iota
	dialingExchangeState
	openedExchangeState
	expiredExchangeState
)

type exchange struct {
	state           exchangeState
	endpoint        *Endpoint
	last_seq        uint32
	next_seq        uint32
	token           cipherset.Token
	hashname        hashname.H
	keys            cipherset.Keys
	parts           cipherset.Parts
	csid            uint8
	cipher          cipherset.State
	qDial           []*opDialExchange
	next_channel_id uint32
	channels        map[uint32]*Channel
	addressBook     *addressBook

	cExchangeWrite chan opExchangeWrite
	cExchangeRead  chan opExchangeRead

	nextHandshake     int
	tExpire           *scheduler.Event
	tBreak            *scheduler.Event
	tDeliverHandshake *scheduler.Event
}

type opExchangeWrite struct {
	x    *exchange
	pkt  *lob.Packet
	cErr chan error
}

type opExchangeRead struct {
	pkt *lob.Packet
	err error
}

func newExchange(e *Endpoint) *exchange {
	x := &exchange{
		endpoint:       e,
		channels:       make(map[uint32]*Channel),
		addressBook:    newAddressBook(),
		cExchangeWrite: e.cExchangeWrite,
	}
	x.tExpire = e.scheduler.NewEvent(x.on_expire)
	x.tBreak = e.scheduler.NewEvent(x.on_break)
	x.tDeliverHandshake = e.scheduler.NewEvent(x.on_deliver_handshake)
	return x
}

func (e *exchange) knownKeys() cipherset.Keys {
	return e.keys
}

func (e *exchange) knownParts() cipherset.Parts {
	return e.parts
}

func (e *exchange) received_handshake(op opReceived, handshake cipherset.Handshake) bool {
	// tracef("receiving_handshake(%p) pkt=%v", e, op.pkt)

	var (
		csid = op.pkt.Head[0]
		seq  = binary.BigEndian.Uint32(op.pkt.Body[:4])
		err  error
	)

	if seq < e.last_seq {
		return false
	}

	if e.cipher == nil {
		key := e.endpoint.key_for_cs(csid)
		if key == nil {
			return false
		}

		e.cipher, err = cipherset.NewState(csid, key)
		if err != nil {
			return false
		}

		e.csid = csid
	}

	if csid != e.csid {
		return false
	}

	if !e.cipher.ApplyHandshake(handshake) {
		return false
	}

	if e.keys == nil {
		e.keys = cipherset.Keys{e.csid: handshake.PublicKey()}
	}
	if e.parts == nil {
		e.parts = handshake.Parts()
	}

	if seq > e.last_seq {
		e.deliver_handshake(seq, op.Src)
		e.addressBook.AddAddress(op.Src)
	} else {
		e.addressBook.ReceivedHandshake(op.Src)
	}

	e.state = openedExchangeState
	e.reset_break()
	for _, op := range e.qDial {
		op.cErr <- nil
	}
	e.qDial = nil

	return true
}

func (e *exchange) deliver_handshake(seq uint32, addr transports.Addr) error {
	// tracef("delivering_handshake(%p)", e)

	var (
		o     = &lob.Packet{Head: []byte{e.csid}}
		addrs []transports.Addr
		err   error
	)

	if seq == 0 {
		seq = e.getNextSeq()
	}

	if addr != nil {
		addrs = append(addrs, addr)
	} else {
		addrs = e.addressBook.HandshakeAddresses()
		e.addressBook.NextHandshakeEpoch()
	}

	o.Body, err = e.cipher.EncryptHandshake(seq, hashname.PartsFromKeys(e.endpoint.keys))
	if err != nil {
		return err
	}

	for _, addr := range addrs {
		e.addressBook.SentHandshake(addr)
		err = e.endpoint.deliver(o, addr) // ignore error
		if err != nil {
			tracef("error: %s %s", addr, err)
		}
	}

	e.last_seq = seq

	// determine when the next handshake must be send
	if addr == nil {
		e.reschedule_handshake()
	}

	return nil
}

func (e *exchange) reschedule_handshake() {
	if e.nextHandshake <= 0 {
		e.nextHandshake = 1
	} else if e.nextHandshake > 60 {
		e.nextHandshake = 60
	} else {
		e.nextHandshake = e.nextHandshake * 2
	}

	if n := e.nextHandshake / 3; n > 0 {
		e.nextHandshake -= rand.Intn(n)
	}

	e.tDeliverHandshake.ScheduleAfter(time.Duration(e.nextHandshake) * time.Second)
}

func (e *exchange) received_packet(pkt *lob.Packet) {
	pkt, err := e.cipher.DecryptPacket(pkt)
	if err != nil {
		return // drop
	}
	var (
		cid, hasC    = pkt.Header().GetUint32("c")
		typ, hasType = pkt.Header().GetString("type")
		_, hasSeq    = pkt.Header().GetUint32("seq")
	)

	if !hasC {
		// drop: missign "c"
		tracef("drop // no `c`")
		return
	}

	c := e.channels[cid]
	if c == nil {
		if !hasType {
			tracef("drop // no `type`")
			return // drop (missing typ)
		}

		h := e.endpoint.handlers[typ]
		if h == nil {
			tracef("drop // no handler for `%s`", typ)
			return // drop (no handler)
		}

		c = newChannel(e.hashname, typ, hasSeq, true)
		c.id = cid
		err = e.register_channel(c)
		if err != nil {
			return // drop (register failed)
		}

		go h.ServeTelehash(c)
	}

	c.received_packet(pkt)
}

func (e *exchange) deliver_packet(op opExchangeWrite) {
	pkt, err := e.cipher.EncryptPacket(op.pkt)
	if err != nil {
		if op.cErr != nil {
			op.cErr <- err
		}
		return
	}

	addr := e.addressBook.ActiveAddress()

	err = e.endpoint.deliver(pkt, addr)
	if err != nil {
		if op.cErr != nil {
			op.cErr <- err
		}
		return
	}

	if op.cErr != nil {
		op.cErr <- nil
	}
	return
}

func (e *exchange) expire(err error) {
	if e.state == expiredExchangeState {
		return
	}

	tracef("expire(%p, %q)", e, err)
	e.state = expiredExchangeState

	// cancel schedule
	e.tExpire.Cancel()
	e.tBreak.Cancel()
	e.tDeliverHandshake.Cancel()

	// unregister
	delete(e.endpoint.hashnames, e.hashname)
	delete(e.endpoint.tokens, e.token)

	// break channels
	for _, c := range e.channels {
		c.on_close_deadline_reached()
	}

	e.endpoint.subscribers.Emit(&ExchangeClosedEvent{e.hashname, err})
}

func (e *exchange) getNextSeq() uint32 {
	seq := e.next_seq
	if n := uint32(time.Now().Unix()); seq < n {
		seq = n
	}
	if seq < e.last_seq {
		seq = e.last_seq + 1
	}
	if seq == 0 {
		seq++
	}

	if e.cipher.IsHigh() {
		// must be odd
		if seq%2 == 0 {
			seq++
		}
	} else {
		// must be even
		if seq%2 == 1 {
			seq++
		}
	}

	e.next_seq = seq + 2
	return seq
}

func (e *exchange) reset_expire() {
	if false /* has channels*/ {
		e.tExpire.Cancel()
	} else {
		e.tExpire.ScheduleAfter(2 * time.Minute)
	}
}

func (e *exchange) on_expire() {
	e.expire(nil)
}

func (e *exchange) reset_break() {
	e.tBreak.ScheduleAfter(2 * time.Minute)
}

func (e *exchange) on_break() {
	e.expire(BrokenExchange(e.hashname))
}

func (e *exchange) on_deliver_handshake() {
	e.deliver_handshake(0, nil)
}
